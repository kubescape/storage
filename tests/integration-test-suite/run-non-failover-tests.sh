#!/bin/bash

set +ex

# List of non-failover tests to run in parallel
NON_FAILOVER_TESTS="TestSimpleProfileCreate TestJobProfileCreate TestCronJobProfileCreate TestInitContainerProfileCreate TestSidecarProfileCreate TestInitSidecarProfileCreate TestCrashLoopProfileIncomplete TestCrashLoopAndStableProfileIncomplete TestStatefulSetProfileCleanup"

# Build the test suite so we don't compile it for each test
go build .

# Arrays to store process IDs and test results
declare -A pids
declare -A results

# Optional extra arguments to pass to the test binary (e.g., "--skip-ensure-helm")
# Set via environment variable EXTRA_TEST_ARGS (unquoted, words are split), for example:
#   EXTRA_TEST_ARGS="--skip-ensure-helm --update-helm-if-present=false" ./run-non-failover-tests.sh
EXTRA_TEST_ARGS="${EXTRA_TEST_ARGS:-}"
echo "Extra test args: $EXTRA_TEST_ARGS"

# Run tests in parallel
for test in $NON_FAILOVER_TESTS; do
    echo "Running $test"
    gotestsum --format standard-verbose --junitfile "junit-$test.xml" -- -count=1 -timeout 30m -v -run "IntegrationTestSuite/$test$" ./... -- $EXTRA_TEST_ARGS 2>&1 | tee "log-$test.txt" &
    pids[$test]=$!
done

# Wait for all tests to complete
for test in $NON_FAILOVER_TESTS; do
    wait ${pids[$test]}
    results[$test]=$?
    echo "Test $test completed with status ${results[$test]}"
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
