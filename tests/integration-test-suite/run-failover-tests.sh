#!/bin/bash

set +e

# List of failover tests to run sequentially
FAILOVER_TESTS="TestSimpleProfileStorageFailover TestSimpleProfileNodeAgentFailover TestLongStorageFailover"

# Array to store test results
declare -A results

# Run tests sequentially
for test in $FAILOVER_TESTS; do
    echo "Running $test"
    gotestsum --junitfile "junit-$test.xml" -- -timeout 30m -v -run "IntegrationTestSuite/$test$" ./... 2>&1 | tee "log-$test.txt"
    results[$test]=$?
done

# Print all test logs
for test in $FAILOVER_TESTS; do
    echo "===== LOG FOR $test ====="
    cat "log-$test.txt"
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