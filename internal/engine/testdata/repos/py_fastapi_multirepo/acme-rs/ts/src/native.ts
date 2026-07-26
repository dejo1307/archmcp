// The prebuilt native binaries this repo publishes ALONGSIDE @acme/sdk. The
// @acme scope is acme-rs's own namespace — it is not the repo labeled "acme",
// even though the leading segment matches.
import { load } from "@acme/native-darwin-arm64";

export const native = load();
