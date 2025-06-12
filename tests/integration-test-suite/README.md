# Integration Test Suite

This directory contains the integration test suite for the Kubescape Storage project.

## Running the tests

To run the tests, use the following command:

```bash
go test -v -timeout 30m -run IntegrationTestSuite/TestSimpleProfileStorageFailover -- --update-if-present --extra-helm-set-args "storage.image.repository=quay.io/matthiasb_1/storage,storage.image.tag=containerprofile@sha256:c2c22c18cdeb2479efb5390f5e11df54985737734e125go test -v -timeout 30m -run IntegrationTestSuite/TestSimpleProfileStorageFailover -- --update-if-present --extra-helm-set-args "storage.image.repository=quay.io/matthiasb_1/storage,storage.image.tag=containerprofile@sha256:c2c22c18cdeb2479efb5390f5e11df54985737734e125c0d2aeb9c4d26ea0bc8,nodeAge.image.tag=test-4447cfd,capabilities.networkEventsStreaming=disable"c0d2aeb9c4d26ea0bc8,nodeAgent.image.tag=test-4447cfd,capabilities.networkEventsStreaming=disable"
```