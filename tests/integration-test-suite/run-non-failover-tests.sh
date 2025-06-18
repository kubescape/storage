#!/bin/bash

set +e

# List of non-failover tests to run in parallel
NON_FAILOVER_TESTS="TestSimpleProfileCreate TestJobProfileCreate TestCronJobProfileCreate TestInitContainerProfileCreate TestSidecarProfileCreate TestInitSidecarProfileCreate TestCrashLoopProfileIncomplete TestCrashLoopAndStableProfileIncomplete TestStatefulSetProfileCleanup"

# Arrays to store process IDs and test results
declare -A pids
declare -A results

# Run tests in parallel
for test in $NON_FAILOVER_TESTS; do
    echo "Running $test"
    gotestsum --junitfile "junit-$test.xml" -- -timeout 30m -v -run "IntegrationTestSuite/$test$" ./... 2>&1 | tee "log-$test.txt" &
    pids[$test]=$!
done

# Wait for all tests to complete
for test in $NON_FAILOVER_TESTS; do
    wait ${pids[$test]}
    results[$test]=$?
done

# Print all test logs
for test in $NON_FAILOVER_TESTS; do
    echo "===== LOG FOR $test ====="
    cat "log-$test.txt"
done

# Check for failures
failed=0
for test in $NON_FAILOVER_TESTS; do
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