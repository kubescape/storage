# ContainerProfile exec and open list semantics

ContainerProfile `execs` can contain the same executable path with different
arguments, and `opens` can contain the same file path with different flags.
These are distinct profile entries. A list keyed only by `path` rejects these
valid profiles during the apiserver's managed-fields conversion.

The OpenAPI schema therefore marks `execs` and `opens` as atomic lists, both
in the top-level spec and within regular, init, and ephemeral containers.
Managed-fields conversion preserves all entries. No fields or protobuf wire
numbers change.

## Apply and patch compatibility

Server-side apply owns each entire `execs` or `opens` list rather than individual
entries keyed by path. Managers editing different entries in the same list can
therefore conflict. Strategic merge patches replace the whole list instead of
merging entries by path; clients must send every entry they intend to retain.

Existing objects may carry managed fields recorded under the previous map-list
schema. This change does not rewrite that ownership history. Maintainers should
review migration behavior for existing field managers and clients using
strategic merge patches before rollout; clean-schema conversion tests do not
establish compatibility with every existing ownership history.

## Regression coverage

`TestContainerProfile_RepeatedExecAndOpenPaths_SMDConversion` and
`TestContainerProfile_RepeatedPathsInContainerGroups_SMDConversion` convert the
generated schema through structured-merge-diff and verify exact preservation of
the profile data, including repeated paths. `TestContainerProfile_ExecAndOpenListsAreAtomic`
pins the atomic extension and absence of patch merge directives for all four
schema properties.
