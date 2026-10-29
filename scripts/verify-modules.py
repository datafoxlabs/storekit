#!/usr/bin/env python3
"""Verify three libraries and the test-only integration module.

--tidy updates module metadata using the same isolated candidate graph. It does not
publish anything. The default verifies tidy/no-diff, unit tests, and a consumer
which downloads every candidate from a temporary Go proxy.
"""

import argparse
import json
from contextlib import contextmanager
import uuid
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import zipfile


ROOT = Path(__file__).resolve().parents[1]
BASE = "github.com/datafoxlabs/storekit"
# Dependency order and local verification versions. Never publish these tags.
MODULES = {
    "clickhouse/schema": "v0.3.1-0.local",
    "migrator": "v0.1.1-0.local",
    "sqlstore": "v0.1.1-0.local",
}


def run(args, cwd=ROOT, env=None, capture=False):
    return subprocess.run(
        args, cwd=cwd, env=env, check=True, text=True,
        stdout=subprocess.PIPE if capture else None,
    ).stdout


def copy_module(relative, destination):
    paths = run(
        ["git", "ls-files", "--cached", "--others", "--exclude-standard", "-z", "--", relative],
        capture=True,
    ).split("\0")
    nested = [path + "/" for path in MODULES if path.startswith(relative + "/")]
    for path in sorted(set(paths)):
        source = ROOT / path
        if not source.is_file() or any(path.startswith(prefix) for prefix in nested):
            continue
        target = destination / source.relative_to(ROOT / relative)
        target.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(source, target)


def publish_candidate(relative, version, source, proxy):
    module = BASE + "/" + relative
    target = proxy / module / "@v"
    target.mkdir(parents=True, exist_ok=True)
    (target / "list").write_text(version + "\n")
    (target / (version + ".info")).write_text(json.dumps({
        "Version": version, "Time": "2000-01-01T00:00:00Z",
    }))
    shutil.copyfile(source / "go.mod", target / (version + ".mod"))
    with zipfile.ZipFile(target / (version + ".zip"), "w", zipfile.ZIP_DEFLATED) as archive:
        for path in sorted(source.rglob("*")):
            if path.is_file():
                archive.write(path, module + "@" + version + "/" + path.relative_to(source).as_posix())


def verify_dependency_boundary(source, env):
    metadata = json.loads(run(["go", "mod", "edit", "-json"], source, env, capture=True))
    if metadata.get("Replace"):
        raise RuntimeError(f"{source.name}: standalone module must not contain replace directives")
    allowed = {BASE + "/" + path for path in MODULES}
    if metadata["Module"]["Path"] == BASE + "/integration":
        allowed.add(BASE + "/integration")
    dependencies = run(["go", "list", "-m", "-f", "{{.Path}}", "all"], source, env, capture=True)
    for module in dependencies.splitlines():
        if module.startswith("github.com/datafoxlabs/") and module not in allowed:
            raise RuntimeError(f"unexpected application dependency: {module}")


def download_pinned_dependencies(cache, env):
    # Candidates use the temporary proxy; declared historical versions must come
    # from Git, even when a runner injects a third-party-only GOPROXY.
    candidates = {BASE + "/" + path: version for path, version in MODULES.items()}
    pins = set()
    for relative in (*MODULES, "integration"):
        metadata = json.loads(run(["go", "mod", "edit", "-json"], ROOT / relative, env, capture=True))
        for dependency in metadata.get("Require") or []:
            path, version = dependency["Path"], dependency["Version"]
            if not path.startswith(BASE + "/"):
                continue
            if path not in candidates:
                raise RuntimeError(f"unexpected storekit dependency: {path}")
            if version != candidates[path]:
                pins.add((path, version))
    download_env = env | {"GOMODCACHE": str(cache), "GONOPROXY": BASE, "GONOSUMDB": BASE}
    for path, version in sorted(pins):
        run(["go", "mod", "download", "-json", path + "@" + version], env=download_env, capture=True)
        print(f"Verified pinned dependency: {path}@{version}", flush=True)


def normalize_metadata(relative, source, env, write):
    # Local candidate checksums change with their working-tree sources.
    # Only --tidy permits refreshing them; the default must detect that drift.
    sums = source / "go.sum"
    if write and sums.exists():
        sums.write_text("".join(line for line in sums.read_text().splitlines(keepends=True)
                                if not line.startswith(BASE + "/")))
    run(["go", "mod", "tidy"], source, env)
    for name in ("go.mod", "go.sum"):
        candidate, original = source / name, ROOT / relative / name
        content = candidate.read_bytes() if candidate.exists() else None
        existing = original.read_bytes() if original.exists() else None
        if content == existing:
            continue
        if not write:
            raise RuntimeError(f"{relative}/{name} is not tidy; run this script with --tidy")
        if content is None:
            original.unlink()
        else:
            original.write_bytes(content)


def verify_driver_isolation(consumer, env):
    for driver in ("", "mysql", "postgres", "sqlite", "clickhouse"):
        directory = consumer / ("probe_" + (driver or "core"))
        directory.mkdir()
        import_path = BASE + "/sqlstore" + ("/" + driver if driver else "")
        (directory / "main.go").write_text(
            'package main\nimport ("fmt"; selected "' + import_path
            + '")\nfunc main() { fmt.Printf("%T", selected.Open) }\n'
        )
        dependencies = run(["go", "list", "-deps", "."], directory, env, capture=True).splitlines()
        actual = {path for path in dependencies if path.startswith("gorm.io/driver/")}
        expected = {"gorm.io/driver/" + driver} if driver else set()
        if actual != expected:
            raise RuntimeError(f"{driver or 'core'} links unexpected drivers: {actual}")
        run(["go", "build", "-mod=readonly", "-o", "probe", "."], directory, env)
        info = run(["go", "version", "-m", "probe"], directory, env, capture=True)
        linked = {line.split()[1] for line in info.splitlines()
                  if line.strip().startswith("dep\tgorm.io/driver/")}
        if linked != expected:
            raise RuntimeError(f"{driver or 'core'} binary has unexpected drivers: {linked}")
        print(f"PASS: {driver or 'core'} driver dependency and binary isolation", flush=True)


@contextmanager
def database_fixtures(env):
    # All servers are disposable, loopback-only, and use synthetic credentials.
    fixtures = (
        ("postgres", "postgres:16", 5432, ["POSTGRES_PASSWORD=fixture-only", "POSTGRES_DB=storekit"],
         "host=127.0.0.1 port={port} user=postgres password=fixture-only dbname=storekit sslmode=disable"),
        ("mysql", "mysql:8.2", 3306, ["MYSQL_ROOT_PASSWORD=fixture-only", "MYSQL_DATABASE=storekit"],
         "root:fixture-only@tcp(127.0.0.1:{port})/storekit?parseTime=true"),
        ("clickhouse", "clickhouse/clickhouse-server:26.2-alpine", 9000,
         ["CLICKHOUSE_USER=fixture", "CLICKHOUSE_PASSWORD=fixture-only", "CLICKHOUSE_DB=storekit"],
         "clickhouse://fixture:fixture-only@127.0.0.1:{port}/storekit"),
    )
    names = []
    result = env.copy()
    try:
        for driver, image, port, variables, dsn in fixtures:
            name = "storekit-verify-" + driver + "-" + uuid.uuid4().hex[:8]
            names.append(name)
            command = ["docker", "run", "-d", "--rm", "--name", name, "-p", f"127.0.0.1::{port}"]
            for variable in variables:
                command += ["-e", variable]
            run(command + [image], capture=True)
            mapped = run(["docker", "port", name, str(port)], capture=True).strip().rsplit(":", 1)[1]
            result["STOREKIT_TEST_" + driver.upper() + "_DSN"] = dsn.format(port=mapped)
            if driver in ("postgres", "mysql"):
                prefix = driver.upper() + "_"
                result.update({prefix + key: value for key, value in {
                    "HOST": "127.0.0.1", "PORT": mapped,
                    "USER": "postgres" if driver == "postgres" else "root",
                    "PASSWORD": "fixture-only", "DB": "storekit", "SSL": "disable",
                }.items()})
            identity = run(["docker", "inspect", "--format", "{{.Image}}", name], capture=True).strip()
            digests = json.loads(run(["docker", "image", "inspect", "--format", "{{json .RepoDigests}}", identity], capture=True))
            if not digests:
                raise RuntimeError(f"fixture {image} has no immutable repository digest")
            print(f"Fixture {driver}: {image} ({identity}); repository digests: {digests}", flush=True)
        yield result
    finally:
        for name in reversed(names):
            subprocess.run(["docker", "rm", "-f", "-v", name], stdout=subprocess.DEVNULL, check=False)


def ci_service_env(env):
    # Only ephemeral service aliases from .github/workflows/verify.yml; never consume a host DSN.
    return env | {
        "STOREKIT_TEST_POSTGRES_DSN": "host=postgres port=5432 user=postgres password=fixture-only dbname=storekit sslmode=disable",
        "STOREKIT_TEST_MYSQL_DSN": "root:fixture-only@tcp(mysql:3306)/storekit?parseTime=true",
        "STOREKIT_TEST_CLICKHOUSE_DSN": "clickhouse://fixture:fixture-only@clickhouse:9000/storekit",
        "POSTGRES_HOST": "postgres", "POSTGRES_PORT": "5432", "POSTGRES_USER": "postgres",
        "POSTGRES_PASSWORD": "fixture-only", "POSTGRES_DB": "storekit", "POSTGRES_SSL": "disable",
        "MYSQL_HOST": "mysql", "MYSQL_PORT": "3306", "MYSQL_USER": "root",
        "MYSQL_PASSWORD": "fixture-only", "MYSQL_DB": "storekit",
    }


def verify_services(source, env):
    run(["go", "test", "-mod=readonly", "-tags", "integration", "-count=1", "-race",
         "-timeout=240s", "-v", "-run", "TestDriverIntegration", "."], source, env)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--tidy", action="store_true", help="update independent module go.mod/go.sum")
    integration = parser.add_mutually_exclusive_group()
    integration.add_argument("--ci-services", action="store_true", help="use the isolated services declared in GitHub Actions")
    integration.add_argument("--integration", action="store_true", help="test all drivers against disposable local Docker servers")
    args = parser.parse_args()
    if args.ci_services and os.environ.get("GITHUB_ACTIONS") != "true":
        parser.error("--ci-services requires GITHUB_ACTIONS=true and isolated GitHub Actions job services")
    settings = json.loads(run(["go", "env", "-json", "GOMODCACHE", "GOPROXY"], capture=True))
    with tempfile.TemporaryDirectory(prefix="storekit-modules-") as temporary:
        work = Path(temporary)
        proxy = work / "proxy"
        proxy.mkdir()
        # Reuse only downloaded third-party archives, not extracted modules or
        # stale candidate versions. Each run has a new module cache.
        cached_proxy = Path(settings["GOMODCACHE"]) / "cache" / "download"
        pinned_cache = work / "pinned-cache"
        download_pinned_dependencies(pinned_cache, os.environ | {"GOWORK": "off", "GOFLAGS": "", "GOTOOLCHAIN": "go1.25.8"})
        env = os.environ | {
            "GOWORK": "off", "GOFLAGS": "", "GOTOOLCHAIN": "go1.25.8",
            "GOMODCACHE": str(work / "modcache"),
            "GOPROXY": ",".join((proxy.as_uri(), (pinned_cache / "cache/download").as_uri(), cached_proxy.as_uri(), settings["GOPROXY"])),
            "GONOPROXY": "none", "GONOSUMDB": BASE,
        }
        for driver in ("POSTGRES", "MYSQL", "CLICKHOUSE"):
            env.pop("STOREKIT_TEST_" + driver + "_DSN", None)
        for relative, version in MODULES.items():
            print(f"Verifying {relative}@{version}", flush=True)
            source = work / "sources" / relative.replace("/", "_")
            copy_module(relative, source)
            normalize_metadata(relative, source, env, args.tidy)
            verify_dependency_boundary(source, env)
            publish_candidate(relative, version, source, proxy)
            run(["go", "vet", "./..."], source, env)
            run(["go", "test", "-mod=readonly", "-count=1", "-race", "-timeout=180s", "./..."], source, env)

        consumer = work / "integration"
        copy_module("integration", consumer)
        normalize_metadata("integration", consumer, env, args.tidy)
        verify_dependency_boundary(consumer, env)
        run(["go", "vet", "./..."], consumer, env)
        run(["go", "test", "-mod=readonly", "-count=1", "-race", "-timeout=180s", "./..."], consumer, env)
        verify_driver_isolation(consumer, env)
        if args.integration or args.ci_services:
            run(["go", "vet", "-tags", "integration", "./..."], consumer, env)
            if args.ci_services:
                verify_services(consumer, ci_service_env(env))
            else:
                with database_fixtures(env) as fixture_env:
                    verify_services(consumer, fixture_env)
        print("PASS: three library modules and test-only integration; no workspace, replace, or application dependency.")


if __name__ == "__main__":
    main()
