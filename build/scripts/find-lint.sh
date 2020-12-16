#!/bin/bash -eu

# Runs the linters against complement

# The linters can take a lot of resources and are slow, so they can be
# configured using the following environment variables:
#
# - `COMPLEMENT_LINT_CONCURRENCY` - number of concurrent linters to run,
#   golangci-lint defaults this to NumCPU
# - `GOGC` - how often to perform garbage collection during golangci-lint runs.
#   Essentially a ratio of memory/speed. See https://golangci-lint.run/usage/performance/#memory-usage
#   for more info.


cd `dirname $0`/..

args=""
if [ ${1:-""} = "fast" ]
then args="--fast"
fi

if [[ -v COMPLEMENT_LINT_CONCURRENCY ]]; then
  args="${args} --concurrency $COMPLEMENT_LINT_CONCURRENCY"
fi

echo "Installing golangci-lint..."

# Make a backup of go.{mod,sum} first
cp go.mod go.mod.bak && cp go.sum go.sum.bak
go get github.com/golangci/golangci-lint/cmd/golangci-lint@v1.33.0
echo ""

# Build the code
# This shouldn't be required, but can help eliminate errors.
# See https://github.com/golangci/golangci-lint/issues/825 for an error that can occur if
# the code isn't build first.
echo "Building complement..."
go build -tags="*" ./...

# Capture exit code to ensure go.{mod,sum} is restored before exiting
exit_code=0

# Run linting
echo "Looking for lint..."
(golangci-lint run $args ./internal/... ./tests/... && echo "No issues found :)") || exit_code=1

# Restore go.{mod,sum}
mv go.mod.bak go.mod && mv go.sum.bak go.sum

exit $exit_code
