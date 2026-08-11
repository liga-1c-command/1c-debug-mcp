package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/1c-debug-mcp/go/internal/client"
	"github.com/1c-debug-mcp/go/internal/events"
	"github.com/1c-debug-mcp/go/internal/logger"
	"github.com/1c-debug-mcp/go/internal/metadata"
	"github.com/1c-debug-mcp/go/internal/ping"
	"github.com/1c-debug-mcp/go/internal/session"
	"github.com/1c-debug-mcp/go/internal/tools"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func main() {
	defer func() {
		if r := recover(); r != nil {
			logger.Error("panic: %v", r)
			os.Exit(1)
		}
	}()

	logger.Init()
	cfg := parseConfig()

	// Create components
	sessionMgr := session.New()
	eventQueue := events.New()
	debugClient := client.New()
	pingLoop := ping.New()
	metaProvider := metadata.New()

	deps := &tools.Deps{
		Client:   debugClient,
		Session:  sessionMgr,
		Ping:     pingLoop,
		Queue:    eventQueue,
		Metadata: metaProvider,
		Config:   cfg,
	}

	// Start metadata loading in background (non-blocking)
	if cfg.CFPath != "" || len(cfg.CFEPaths) > 0 || len(cfg.EPFPaths) > 0 {
		metaProvider.Load(cfg.CFPath, cfg.CFEPaths, cfg.EPFPaths, cfg.CachePath, cfg.DisableCache)
	}

	// Log config
	if cfg.URL != "" && cfg.Alias != "" {
		logger.Info("Config: url=%s, alias=%s, cfPath=%s",
			cfg.URL, cfg.Alias, orStr(cfg.CFPath, "not set"))
	} else {
		logger.Error("WARNING: url or alias not configured. Set ONEC_DEBUG_URL and ONEC_INFOBASE_ALIAS in mcp.json env section.")
	}

	// Create MCP server
	mcpServer := server.NewMCPServer("1c-debug", "1.0.0")

	// Register all tools
	registerTools(mcpServer, deps)

	// Handle graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		logger.Info("Shutting down...")
		pingLoop.Stop()
		if s := sessionMgr.Get(); s != nil {
			_ = debugClient.Detach(s)
			sessionMgr.Clear()
		}
		os.Exit(0)
	}()

	logger.Info("MCP server started")

	// Start stdio server (blocks until stdin closes)
	if err := server.ServeStdio(mcpServer); err != nil {
		logger.Error("Server error: %v", err)
		os.Exit(1)
	}
}

func registerTools(s *server.MCPServer, deps *tools.Deps) {
	// attach
	s.AddTool(mcp.NewTool("attach",
		mcp.WithDescription("Connect to 1C debug server (dbgs.exe)"),
		mcp.WithString("url", mcp.Description("Debug server URL, e.g. http://localhost:1550 (default from ONEC_DEBUG_URL)")),
		mcp.WithString("infobaseAlias", mcp.Description("Infobase alias, e.g. DefAlias (default from ONEC_INFOBASE_ALIAS)")),
		mcp.WithBoolean("autoAttach", mcp.Description("Auto-attach to all debug targets (default: true)")),
		mcp.WithString("password", mcp.Description("Debug server password")),
	), func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return tools.HandleAttach(deps, req.GetArguments()), nil
	})

	// detach
	s.AddTool(mcp.NewTool("detach",
		mcp.WithDescription("Disconnect from 1C debug server"),
	), func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return tools.HandleDetach(deps, req.GetArguments()), nil
	})

	// force_detach
	s.AddTool(mcp.NewTool("force_detach",
		mcp.WithDescription("Forcefully stop ping loop and clear session (use when ibInDebug or session is stuck)"),
	), func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return tools.HandleForceDetach(deps, req.GetArguments()), nil
	})

	// get_targets
	s.AddTool(mcp.NewTool("get_targets",
		mcp.WithDescription("Get list of connected debug targets (1C processes)"),
	), func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return tools.HandleGetTargets(deps, req.GetArguments()), nil
	})

	// set_breakpoints
	s.AddTool(mcp.NewTool("set_breakpoints",
		mcp.WithDescription("Set breakpoints in a BSL module"),
		mcp.WithString("moduleName", mcp.Required(), mcp.Description("Module name, e.g. МойОбщийМодуль")),
		mcp.WithString("moduleType", mcp.Description("Module type: CommonModule, ObjectModule, FormModule, ManagerModule, etc.")),
		mcp.WithArray("lines", mcp.Required(), mcp.Description("Line numbers to set breakpoints on")),
		mcp.WithString("objectID", mcp.Description("Object GUID from metadata. Auto-resolved if not provided.")),
		mcp.WithString("extensionName", mcp.Description("Extension name to search in. Empty means main configuration.")),
		mcp.WithString("targetId", mcp.Description("Debug target ID")),
	), func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return tools.HandleSetBreakpoints(deps, req.GetArguments()), nil
	})

	// clear_breakpoints
	s.AddTool(mcp.NewTool("clear_breakpoints",
		mcp.WithDescription("Clear all breakpoints"),
	), func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return tools.HandleClearBreakpoints(deps, req.GetArguments()), nil
	})

	// continue
	s.AddTool(mcp.NewTool("continue",
		mcp.WithDescription("Continue execution of a stopped debug target"),
		mcp.WithString("targetId", mcp.Required(), mcp.Description("Debug target ID from get_targets")),
	), func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return tools.HandleContinue(deps, req.GetArguments()), nil
	})

	// step_in
	s.AddTool(mcp.NewTool("step_in",
		mcp.WithDescription("Step into the next statement (enters procedures/functions)"),
		mcp.WithString("targetId", mcp.Required(), mcp.Description("Debug target ID")),
	), func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return tools.HandleStepIn(deps, req.GetArguments()), nil
	})

	// step_out
	s.AddTool(mcp.NewTool("step_out",
		mcp.WithDescription("Step out of the current procedure/function"),
		mcp.WithString("targetId", mcp.Required(), mcp.Description("Debug target ID")),
	), func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return tools.HandleStepOut(deps, req.GetArguments()), nil
	})

	// pause
	s.AddTool(mcp.NewTool("pause",
		mcp.WithDescription("Pause execution on the next BSL statement (sets breakOnNextLine=true)"),
		mcp.WithString("targetId", mcp.Description("Debug target ID (optional, for compatibility)")),
	), func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return tools.HandlePause(deps, req.GetArguments()), nil
	})

	// wait_for_stop
	s.AddTool(mcp.NewTool("wait_for_stop",
		mcp.WithDescription("Wait until a debug target stops (breakpoint or step)"),
		mcp.WithNumber("timeout", mcp.Description("Timeout in milliseconds (default: 30000)")),
	), func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return tools.HandleWaitForStop(deps, req.GetArguments()), nil
	})

	// get_variables
	s.AddTool(mcp.NewTool("get_variables",
		mcp.WithDescription("Get local variables of a stopped debug target"),
		mcp.WithString("targetId", mcp.Required(), mcp.Description("Debug target ID")),
	), func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return tools.HandleGetVariables(deps, req.GetArguments()), nil
	})

	// evaluate
	s.AddTool(mcp.NewTool("evaluate",
		mcp.WithDescription("Evaluate a BSL expression in the context of a stopped debug target"),
		mcp.WithString("targetId", mcp.Required(), mcp.Description("Debug target ID")),
		mcp.WithString("expression", mcp.Required(), mcp.Description("BSL expression to evaluate")),
	), func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return tools.HandleEvaluate(deps, req.GetArguments()), nil
	})

	// raw_request
	s.AddTool(mcp.NewTool("raw_request",
		mcp.WithDescription("Send a raw XML request to the 1C debug server"),
		mcp.WithString("cmd", mcp.Required(), mcp.Description("Command name, e.g. attachDetachDbgTargets, setBreakpoints")),
		mcp.WithString("xml", mcp.Required(), mcp.Description("Full XML body to send")),
		mcp.WithString("dbgui", mcp.Description("Optional dbgui query parameter")),
	), func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return tools.HandleRawRequest(deps, req.GetArguments()), nil
	})

	// reload_metadata
	s.AddTool(mcp.NewTool("reload_metadata",
		mcp.WithDescription("Reload metadata from source files (use after updating configuration sources)"),
		mcp.WithBoolean("skipCache", mcp.Description("Skip cache and force full rescan (default: false)")),
	), func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return tools.HandleReloadMetadata(deps, req.GetArguments()), nil
	})

	// get_call_stack
	s.AddTool(mcp.NewTool("get_call_stack",
		mcp.WithDescription("Get call stack of a stopped debug target (from last stop event)"),
		mcp.WithString("targetId", mcp.Required(), mcp.Description("Debug target ID")),
	), func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return tools.HandleGetCallStack(deps, req.GetArguments()), nil
	})
}

// parseConfig reads configuration from environment variables and CLI flags.
// CLI flags take priority over environment variables.
func parseConfig() *tools.Config {
	var urlFlag, aliasFlag, passwordFlag, cfPathFlag, cachePathFlag string

	flag.StringVar(&urlFlag, "url", "", "Debug server URL (overrides ONEC_DEBUG_URL)")
	flag.StringVar(&aliasFlag, "alias", "", "Infobase alias (overrides ONEC_INFOBASE_ALIAS)")
	flag.StringVar(&passwordFlag, "password", "", "Debug server password (overrides ONEC_DEBUG_PASSWORD)")
	flag.StringVar(&cfPathFlag, "cf-path", "", "Path to configuration sources (overrides ONEC_CF_PATH)")
	flag.StringVar(&cachePathFlag, "cache-path", "", "Path to metadata cache file or directory (overrides ONEC_CACHE_PATH)")
	flag.Parse()

	cfg := &tools.Config{
		URL:          orStr(urlFlag, os.Getenv("ONEC_DEBUG_URL")),
		Alias:        orStr(aliasFlag, os.Getenv("ONEC_INFOBASE_ALIAS")),
		Password:     orStr(passwordFlag, os.Getenv("ONEC_DEBUG_PASSWORD")),
		CFPath:       orStr(cfPathFlag, os.Getenv("ONEC_CF_PATH")),
		CFEPaths:     splitPaths(os.Getenv("ONEC_CFE_PATHS")),
		EPFPaths:     splitPaths(os.Getenv("ONEC_EPF_PATHS")),
		CachePath:    orStr(cachePathFlag, os.Getenv("ONEC_CACHE_PATH")),
		DisableCache: os.Getenv("ONEC_DISABLE_CACHE") == "true",
	}
	return cfg
}

func orStr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func splitPaths(s string) []string {
	if s == "" {
		return nil
	}
	var parts []string
	for _, p := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ';' }) {
		p = strings.TrimSpace(p)
		if p != "" {
			parts = append(parts, p)
		}
	}
	return parts
}
