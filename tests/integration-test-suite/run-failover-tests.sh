#!/bin/bash

set +e

# List of failover tests to run sequentially
FAILOVER_TESTS="TestSimpleProfileStorageFailover TestSimpleProfileNodeAgentFailover TestLongStorageFailover TestScaleDeploymentPartialCompletion"

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
