#!/bin/bash

set +e

# Ensure we're running with bash (not zsh or other shells)
if [ -z "$BASH_VERSION" ]; then
    echo "Error: This script requires bash. Please run with: bash $0"
    exit 1
fi

# Set GOPATH if not already set (default Go location)
if [ -z "$GOPATH" ]; then
    export GOPATH="$HOME/go"
fi

# Add Go bin directories to PATH if not already present
if [ -n "$GOPATH" ] && [ -d "$GOPATH/bin" ] && [[ ":$PATH:" != *":$GOPATH/bin:"* ]]; then
    export PATH="$GOPATH/bin:$PATH"
fi
if [ -n "$GOBIN" ] && [ -d "$GOBIN" ] && [[ ":$PATH:" != *":$GOBIN:"* ]]; then
    export PATH="$GOBIN:$PATH"
fi

# Check for gotestsum in common locations
GOTESTSUM_PATH=""
if command -v gotestsum &> /dev/null; then
    GOTESTSUM_PATH=$(command -v gotestsum)
elif [ -f "$GOPATH/bin/gotestsum" ]; then
    GOTESTSUM_PATH="$GOPATH/bin/gotestsum"
    export PATH="$GOPATH/bin:$PATH"
elif [ -f "$HOME/go/bin/gotestsum" ]; then
    GOTESTSUM_PATH="$HOME/go/bin/gotestsum"
    export PATH="$HOME/go/bin:$PATH"
fi

# Check if gotestsum is available
if [ -z "$GOTESTSUM_PATH" ] || [ ! -f "$GOTESTSUM_PATH" ]; then
    echo "Error: gotestsum is not installed."
    echo "Install it with: go install gotest.tools/gotestsum@latest"
    echo "GOPATH: $GOPATH"
    echo "GOBIN: $GOBIN"
    echo "Checked paths: $GOPATH/bin, $HOME/go/bin"
    exit 1
fi

# List of failover tests to run sequentially
FAILOVER_TESTS="TestSimpleProfileNodeAgentFailover"

# Array to store test results
declare -A results

# Run tests sequentially
for test in $FAILOVER_TESTS; do
    echo "Running $test"
    gotestsum --format standard-verbose --junitfile "junit-$test.xml" -- -count=1 -timeout 30m -v -run "IntegrationTestSuite/$test$" ./... 2>&1 | tee "log-$test.txt"
    results[$test]=$?
done

# Print all test logs
for test in $FAILOVER_TESTS; do
    echo "===== LOG FOR $test ====="
    cat "log-$test.txt"
    echo "Test $test completed with status ${results[$test]}"
done

# Check for failures
failed=0
for test in $FAILOVER_TESTS; do
    if [ "${results[$test]}" != "0" ]; then
        echo "Test $test failed"
        failed=1
    else
        # Check junit file for failures as well
        if grep -q 'failures="[1-9]' "junit-$test.xml" || grep -q 'errors="[1-9]' "junit-$test.xml"; then
            echo "Test $test failed according to junit report"
            failed=1
        fi
    fi
done

exit $failed
