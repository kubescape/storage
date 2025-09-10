# Integration Test Suite

This directory contains the integration test suite for the Kubescape Storage project.

## Running the tests

To run the tests, use the following command:

```bash
go test -v -timeout 30m -run IntegrationTestSuite/TestSimpleProfileCreate -- --update-if-present --extra-helm-set-args "storage.image.repository=quay.io/matthiasb_1/storage,storage.image.tag=containerprofile,nodeAgent.image.tag=test-cp-6,capabilities.networkEventsStreaming=disable"
```
