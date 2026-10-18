# #42100 Prefer remote Sites over the bundled plugin

## Upstream change

Rust persists an account/backend-scoped exclusion for `sites@openai-bundled`
once a loadable remote Sites replacement is installed, applies it across
catalog/search/read/install/runtime/hook/skill paths, and restores the bundled
fallback when the remote copy is absent.

## Go architecture/assets

Go's discoverable bundled catalog contains only `chrome@openai-bundled` and
`computer-use@openai-bundled`. There is no bundled Sites entry or copy in
`plugin/discoverable.go`, so there is no duplicate bundled catalog entry to
hide and no exclusion state to persist or restore. Remote plugin routing is the
only Sites path.

## Conclusion

N/A by architecture/assets.
