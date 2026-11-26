Generated Angular build output belongs here. Build `frontend/` and run
`deploy/gateway/build-admin-ui.sh` from the Gateway directory before a native Go
build. The multi-stage Dockerfile builds and embeds the same output automatically.

This placeholder allows backend checks without installing Node dependencies. The
active Admin handler returns an explicit unavailable response until an Angular
index is embedded; first-run setup remains independently available.
