import { defineExtension } from "../../index.js";
import { loadMCPConfig } from "./config.js";
import { registerMCP } from "./register.js";

export default defineExtension(async (ext) => {
  ext.setMetadata({ name: "kodelet-mcp" });

  const cwd = process.cwd();
  const config = await loadMCPConfig(cwd);

  await registerMCP(ext, config);
});
