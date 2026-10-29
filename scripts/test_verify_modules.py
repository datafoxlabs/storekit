import importlib.util
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

SPEC = importlib.util.spec_from_file_location("verify_modules", Path(__file__).with_name("verify-modules.py"))
VERIFY = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(VERIFY)


class VerificationBoundaryTests(unittest.TestCase):
    def test_ci_mode_rejects_non_ci_before_subprocesses(self):
        with patch.dict(os.environ, {}, clear=True), patch.object(sys, "argv", ["verify-modules.py", "--ci-services"]):
            with patch.object(VERIFY, "run") as run, self.assertRaises(SystemExit):
                VERIFY.main()
            run.assert_not_called()

    def test_ci_fixtures_override_inherited_targets(self):
        env = VERIFY.ci_service_env({"STOREKIT_TEST_POSTGRES_DSN": "unexpected", "POSTGRES_HOST": "unexpected"})
        self.assertEqual(env["POSTGRES_HOST"], "postgres")
        self.assertIn("host=postgres ", env["STOREKIT_TEST_POSTGRES_DSN"])
        self.assertIn("tcp(mysql:3306)", env["STOREKIT_TEST_MYSQL_DSN"])
        self.assertIn("@clickhouse:9000/", env["STOREKIT_TEST_CLICKHOUSE_DSN"])

    def test_pinned_dependencies_are_downloaded_once_without_candidate_versions(self):
        metadata = json.dumps({"Require": [
            {"Path": VERIFY.BASE + "/clickhouse/schema", "Version": VERIFY.MODULES["clickhouse/schema"]},
            {"Path": VERIFY.BASE + "/migrator", "Version": "v0.0.0-20000101000000-0123456789ab"},
        ]})
        calls = []
        def run(args, *rest, **kwargs):
            calls.append((args, kwargs))
            return metadata if args[:3] == ["go", "mod", "edit"] else "{}"
        with patch.object(VERIFY, "run", side_effect=run):
            VERIFY.download_pinned_dependencies(Path("/tmp/test-pinned-cache"), {"GOPROXY": "https://third-party.invalid"})
        downloads = [(args, kwargs) for args, kwargs in calls if args[:3] == ["go", "mod", "download"]]
        self.assertEqual(len(downloads), 1)
        self.assertEqual(downloads[0][0][-1], VERIFY.BASE + "/migrator@v0.0.0-20000101000000-0123456789ab")
        self.assertEqual(downloads[0][1]["env"]["GONOPROXY"], VERIFY.BASE)

    def test_invalid_pinned_module_is_rejected_before_download(self):
        metadata = json.dumps({"Require": [{"Path": VERIFY.BASE + "/integration", "Version": "v0.1.0"}]})
        with patch.object(VERIFY, "run", return_value=metadata) as run:
            with self.assertRaisesRegex(RuntimeError, "unexpected storekit dependency"):
                VERIFY.download_pinned_dependencies(Path("/tmp/test-pinned-cache"), {})
        self.assertEqual(run.call_count, 1)

    def test_tidy_drift_fails_without_mutating_repository(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            original, source = root / "integration", root / "copy"
            original.mkdir()
            source.mkdir()
            (original / "go.mod").write_text("original\n")
            (source / "go.mod").write_text("changed\n")
            with patch.object(VERIFY, "ROOT", root), patch.object(VERIFY, "run"):
                with self.assertRaisesRegex(RuntimeError, "not tidy"):
                    VERIFY.normalize_metadata("integration", source, {}, False)
            self.assertEqual((original / "go.mod").read_text(), "original\n")

    def test_library_cannot_depend_on_test_module(self):
        metadata = json.dumps({"Module": {"Path": VERIFY.BASE + "/sqlstore"}})
        with patch.object(VERIFY, "run", side_effect=[metadata, VERIFY.BASE + "/integration\n"]):
            with self.assertRaisesRegex(RuntimeError, "unexpected application dependency"):
                VERIFY.verify_dependency_boundary(Path("sqlstore"), {})

    def test_library_cannot_depend_on_sibling_application(self):
        metadata = json.dumps({"Module": {"Path": VERIFY.BASE + "/sqlstore"}})
        for dependency in (VERIFY.BASE, "github.com/datafoxlabs/example-app"):
            with self.subTest(dependency=dependency), patch.object(VERIFY, "run", side_effect=[metadata, dependency + "\n"]):
                with self.assertRaisesRegex(RuntimeError, "unexpected application dependency"):
                    VERIFY.verify_dependency_boundary(Path("sqlstore"), {})

    def test_docker_cleanup_runs_after_fixture_failure(self):
        commands = []

        def run(args, **kwargs):
            if args[:2] == ["docker", "run"]:
                return "container"
            if args[:2] == ["docker", "port"]:
                raise RuntimeError("injected port lookup failure")
            self.fail(f"unexpected command {args}")

        with patch.object(VERIFY, "run", side_effect=run), patch.object(subprocess, "run", side_effect=lambda args, **kwargs: commands.append(args)):
            with self.assertRaisesRegex(RuntimeError, "injected"):
                with VERIFY.database_fixtures({}):
                    self.fail("incomplete fixtures yielded")
        self.assertEqual(len(commands), 1)
        self.assertEqual(commands[0][:4], ["docker", "rm", "-f", "-v"])
        self.assertTrue(commands[0][4].startswith("storekit-verify-postgres-"))


if __name__ == "__main__":
    unittest.main()
