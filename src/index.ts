#!/usr/bin/env node
import { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import { StdioServerTransport } from "@modelcontextprotocol/sdk/server/stdio.js";
import { z } from "zod";
import * as fs from "fs";
import * as path from "path";
import { fileURLToPath } from "url";

// Log to file — overwrite on each start
const logFile = path.join(path.dirname(fileURLToPath(import.meta.url)), "1c-debug.log");
const logStream = fs.createWriteStream(logFile, { flags: "w" });
const origStderr = process.stderr.write.bind(process.stderr);
(process.stderr as NodeJS.WriteStream).write = (chunk: string | Uint8Array, ...args: unknown[]): boolean => {
  logStream.write(chunk);
  return (origStderr as (...a: unknown[]) => boolean)(chunk, ...args);
};

import { DebugClient } from "./debugClient.js";
import { SessionManager } from "./sessionManager.js";
import { PingLoop } from "./pingLoop.js";
import { EventQueue } from "./eventQueue.js";
import { parseConfig } from "./config.js";
import { MetadataProvider } from "./metadataProvider.js";
import { ModuleType } from "./types/requests.js";

import { createAttachTool, createDetachTool } from "./tools/attach.js";
import { createGetTargetsTool } from "./tools/targets.js";
import { createSetBreakpointsTool, createClearBreakpointsTool } from "./tools/breakpoints.js";
import { createContinueTool, createStepInTool, createStepOutTool, createPauseTool } from "./tools/execution.js";
import { createWaitForStopTool } from "./tools/waitForStop.js";
import { createGetCallStackTool, createGetVariablesTool, createEvaluateTool } from "./tools/inspection.js";

// Singletons
const config = parseConfig();
const debugClient = new DebugClient();
const sessionManager = new SessionManager();
const pingLoop = new PingLoop();
const eventQueue = new EventQueue();
// metadata is created empty now, loaded after server.connect() to avoid startup timeout
const metadata = new MetadataProvider();

// Tool handlers
const attachTool = createAttachTool(debugClient, sessionManager, pingLoop, eventQueue, config);
const detachTool = createDetachTool(debugClient, sessionManager, pingLoop);
const getTargetsTool = createGetTargetsTool(debugClient, sessionManager, metadata);
const setBreakpointsTool = createSetBreakpointsTool(debugClient, sessionManager, metadata);
const clearBreakpointsTool = createClearBreakpointsTool(debugClient, sessionManager);
const continueTool = createContinueTool(debugClient, sessionManager);
const stepInTool = createStepInTool(debugClient, sessionManager);
const stepOutTool = createStepOutTool(debugClient, sessionManager);
const pauseTool = createPauseTool(debugClient, sessionManager);
const waitForStopTool = createWaitForStopTool(sessionManager, eventQueue, metadata);
const getCallStackTool = createGetCallStackTool(debugClient, sessionManager, eventQueue);
const getVariablesTool = createGetVariablesTool(debugClient, sessionManager, eventQueue);
const evaluateTool = createEvaluateTool(debugClient, sessionManager, eventQueue);

// MCP Server
const server = new McpServer({
  name: "1c-debug",
  version: "1.0.0",
});

server.tool(
  "attach",
  "Connect to 1C debug server (dbgs.exe)",
  {
    url: z.string().optional().describe("Debug server URL, e.g. http://localhost:1550 (default from ONEC_DEBUG_URL)"),
    infobaseAlias: z.string().optional().describe("Infobase alias, e.g. DefAlias (default from ONEC_INFOBASE_ALIAS)"),
    autoAttach: z.boolean().optional().describe("Auto-attach to all debug targets (default: true)"),
    password: z.string().optional().describe("Debug server password"),
  },
  attachTool,
);

server.tool(
  "detach",
  "Disconnect from 1C debug server",
  {},
  detachTool,
);

server.tool(
  "get_targets",
  "Get list of connected debug targets (1C processes)",
  {},
  getTargetsTool,
);

server.tool(
  "set_breakpoints",
  "Set breakpoints in a BSL module",
  {
    moduleName: z.string().describe("Module name, e.g. МойОбщийМодуль"),
    moduleType: z.string().optional().describe(`Module type: ${Object.values(ModuleType).join(", ")}`),
    lines: z.array(z.number().int().positive()).describe("Line numbers to set breakpoints on"),
    objectID: z.string().optional().describe("Object GUID from metadata (e.g. 4eee25b1-2da6-459b-953b-4c8d519c9bce). Use instead of URL for reliable breakpoint matching."),
    targetId: z.string().optional().describe("Debug target ID — if provided, calls clearBreakOnNextStatement + attachDetachDbgTargets before setting breakpoints"),
  },
  setBreakpointsTool,
);

server.tool(
  "clear_breakpoints",
  "Clear all breakpoints",
  {
    moduleName: z.string().optional(),
    moduleType: z.string().optional(),
  },
  clearBreakpointsTool,
);

server.tool(
  "continue",
  "Continue execution of a stopped debug target",
  {
    targetId: z.string().describe("Debug target ID from get_targets"),
  },
  continueTool,
);

server.tool(
  "step_in",
  "Step into the next statement (enters procedures/functions)",
  {
    targetId: z.string().describe("Debug target ID"),
  },
  stepInTool,
);

server.tool(
  "step_out",
  "Step out of the current procedure/function",
  {
    targetId: z.string().describe("Debug target ID"),
  },
  stepOutTool,
);

server.tool(
  "pause",
  "Pause execution of a debug target on the next statement",
  {
    targetId: z.string().describe("Debug target ID"),
  },
  pauseTool,
);

server.tool(
  "wait_for_stop",
  "Wait until a debug target stops (breakpoint or step)",
  {
    timeout: z.number().optional().describe("Timeout in milliseconds (default: 30000)"),
  },
  waitForStopTool,
);

server.tool(
  "raw_request",
  "Send a raw XML request to the 1C debug server. Use for debugging protocol issues.",
  {
    cmd: z.string().describe("Command name, e.g. attachDetachDbgTargets, setBreakpoints"),
    xml: z.string().describe("Full XML body to send"),
    dbgui: z.string().optional().describe("Optional dbgui query parameter"),
  },
  async (args) => {
    const session = sessionManager.getSession();
    const baseUrl = session?.url ?? "http://localhost:1550";
    let url = `${baseUrl}/e1crdbg/rdbg?cmd=${args.cmd}`;
    if (args.dbgui) url += `&dbgui=${args.dbgui}`;

    try {
      const response = await fetch(url, {
        method: "POST",
        headers: { "Content-Type": "application/xml; charset=utf-8", "Accept": "application/xml", "User-Agent": "1CV8" },
        body: args.xml,
      });
      const text = await response.text();
      return { content: [{ type: "text" as const, text: JSON.stringify({ status: response.status, body: text }) }] };
    } catch (err) {
      return { content: [{ type: "text" as const, text: JSON.stringify({ error: String(err) }) }], isError: true };
    }
  },
);

server.tool(
  "reload_metadata",
  "Reload metadata from source files (use after updating configuration sources)",
  {
    skipCache: z.boolean().optional().describe("Skip cache and force full rescan (default: false)"),
  },
  async (args) => {
    const skipCache = args.skipCache ?? false;
    process.stderr.write(`[1c-debug] Reloading metadata${skipCache ? " (cache bypassed)" : ""}...\n`);
    const result = await metadata.reload(skipCache);
    const msg = `Reloaded ${result.moduleCount} modules${skipCache ? " (cache bypassed)" : ""}`;
    process.stderr.write(`[1c-debug] ${msg}\n`);
    return {
      content: [{ type: "text" as const, text: JSON.stringify({ 
        success: true, 
        moduleCount: result.moduleCount,
        skipCache,
        message: msg,
      }) }],
    };
  },
);

server.tool(
  "get_variables",
  "Get local variables of a stopped debug target",
  {
    targetId: z.string().describe("Debug target ID"),
  },
  getVariablesTool,
);

server.tool(
  "evaluate",
  "Evaluate a BSL expression in the context of a stopped debug target",
  {
    targetId: z.string().describe("Debug target ID"),
    expression: z.string().describe("BSL expression to evaluate"),
  },
  evaluateTool,
);

// Graceful shutdown
async function shutdown() {
  pingLoop.stop();
  const session = sessionManager.getSession();
  if (session) {
    try {
      await debugClient.detach(session);
    } catch {
      // best-effort
    }
    sessionManager.clearSession();
  }
  process.exit(0);
}

process.on("SIGINT", () => void shutdown());
process.on("SIGTERM", () => void shutdown());

// Start
if (config.url && config.alias) {
  process.stderr.write(`[1c-debug] Config: url=${config.url}, alias=${config.alias}, cfPath=${config.cfPath ?? "not set"}\n`);
} else {
  process.stderr.write(`[1c-debug] WARNING: url or alias not configured. Set ONEC_DEBUG_URL and ONEC_INFOBASE_ALIAS in mcp.json env section.\n`);
}

const transport = new StdioServerTransport();
await server.connect(transport);
process.stderr.write("[1c-debug] MCP server started\n");

// Load metadata AFTER server is connected — avoids blocking MCP handshake
setTimeout(() => {
  metadata.load(config.cfPath, config.cfePaths, config.epfPaths).then(() => {
    process.stderr.write("[1c-debug] Metadata loaded and ready\n");
  });
}, 0);
