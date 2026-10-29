.PHONY: test tidy test-integration test-integration-ci check-fmt check-harness check-tools

test: check-fmt check-tools
	python3 scripts/verify-modules.py

tidy:
	python3 scripts/verify-modules.py --tidy

test-integration: check-fmt check-tools
	python3 scripts/verify-modules.py --integration

test-integration-ci: check-fmt check-tools
	python3 scripts/verify-modules.py --ci-services

check-fmt:
	@unformatted="$(find clickhouse migrator sqlstore integration -name '*.go' -exec gofmt -l {} +)"; \
		test -z "$unformatted" || { printf 'Run gofmt on:\\n%s\\n' "$unformatted"; exit 1; }

check-tools:
	python3 -m unittest discover -s scripts -p 'test_*.py' -v

check-harness:
	python3 scripts/verify-harness.py
