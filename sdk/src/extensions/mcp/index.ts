import { defineExtension } from "../../index.js";
import { loadMCPConfig } from "./config.js";
import { registerMCP } from "./register.js";

export default defineExtension(async (ext) => {
  ext.setMetadata({ name: "kodelet-mcp" });

  const config = await loadMCPConfig();

  await registerMCP(ext, config);
});
