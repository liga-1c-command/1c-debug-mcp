package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/1c-debug-mcp/go/internal/client"
	"github.com/1c-debug-mcp/go/internal/events"
	"github.com/1c-debug-mcp/go/internal/logger"
	"github.com/1c-debug-mcp/go/internal/metadata"
	"github.com/1c-debug-mcp/go/internal/ping"
	"github.com/1c-debug-mcp/go/internal/session"
	"github.com/1c-debug-mcp/go/internal/xmlproto"
	"github.com/mark3labs/mcp-go/mcp"
)

// Config holds the default configuration values from environment.
type Config struct {
	URL          string
	Alias        string
	Password     string
	CFPath       string
	CFEPaths     []string
	EPFPaths     []string
	CachePath    string
	DisableCache bool
}

// Deps holds all dependencies for tool handlers.
type Deps struct {
	Client   *client.Client
	Session  *session.Manager
	Ping     *ping.Loop
	Queue    *events.Queue
	Metadata *metadata.Provider
	Config   *Config
}

// toolResult creates a successful MCP tool result with JSON content.
func toolResult(v interface{}) *mcp.CallToolResult {
	b, _ := json.Marshal(v)
	return mcp.NewToolResultText(string(b))
}

// errResult creates an error MCP tool result.
func errResult(msg string) *mcp.CallToolResult {
	return mcp.NewToolResultError(msg)
}

// strArg extracts a string argument from args map.
func strArg(args map[string]interface{}, key string) string {
	if v, ok := args[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// boolArg extracts a bool argument from args map.
func boolArg(args map[string]interface{}, key string, def bool) bool {
	if v, ok := args[key]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return def
}

// intArg extracts an int argument from args map.
func intArg(args map[string]interface{}, key string, def int) int {
	if v, ok := args[key]; ok {
		switch n := v.(type) {
		case float64:
			return int(n)
		case int:
			return n
		}
	}
	return def
}

// HandleAttach connects to the 1C debug server.
func HandleAttach(deps *Deps, args map[string]interface{}) *mcp.CallToolResult {
	url := strArg(args, "url")
	if url == "" {
		url = deps.Config.URL
	}
	alias := strArg(args, "infobaseAlias")
	if alias == "" {
		alias = deps.Config.Alias
	}
	password := strArg(args, "password")
	if password == "" {
		password = deps.Config.Password
	}
	autoAttach := boolArg(args, "autoAttach", true)

	if url == "" || alias == "" {
		return errResult("url and infobaseAlias are required. Set ONEC_DEBUG_URL and ONEC_INFOBASE_ALIAS in mcp.json env section, or pass them explicitly.")
	}

	// If ping loop is currently reconnecting — wait a bit
	if deps.Ping.IsReattaching() {
		return errResult("Debug session is reconnecting, please retry in a few seconds.")
	}

	if err := deps.Client.Test(url); err != nil {
		return errResult(fmt.Sprintf("Debug server unreachable: %v", err))
	}

	s := deps.Session.Create(url, alias, password)

	if err := deps.Client.AttachWithRetry(s); err != nil {
		deps.Session.Clear()
		return errResult(fmt.Sprintf("Attach failed: %v", err))
	}

	_ = deps.Client.InitSettings(s, false)

	if autoAttach {
		_ = deps.Client.SetAutoAttach(s, []string{"Client", "ManagedClient", "Server", "ServerEmulation", "JOB"})
	}

	// Attach existing targets — all at once in one request
	targets, err := deps.Client.GetTargets(s)
	if err == nil && len(targets) > 0 {
		var tids []xmlproto.TargetID
		for _, t := range targets {
			tids = append(tids, t.TargetID)
		}
		_ = deps.Client.AttachDetachTargets(s, tids, true)
	}

	deps.Ping.Start(s, deps.Client, deps.Queue)

	return toolResult(map[string]string{
		"sessionId":     s.ID,
		"url":           url,
		"infobaseAlias": alias,
	})
}

// HandleDetach disconnects from the 1C debug server.
func HandleDetach(deps *Deps, _ map[string]interface{}) *mcp.CallToolResult {
	s, err := deps.Session.Require()
	if err != nil {
		return errResult(err.Error())
	}

	deps.Ping.Stop()
	_ = deps.Client.Detach(s)
	deps.Session.Clear()

	return toolResult(map[string]bool{"success": true})
}

// HandleForceDetach forcefully stops ping loop, detaches all sessions and clears state.
// Use when session is stuck or ibInDebug error occurs.
func HandleForceDetach(deps *Deps, _ map[string]interface{}) *mcp.CallToolResult {
	// Stop ping loop first
	deps.Ping.Stop()

	// Try to detach current session
	if s := deps.Session.Get(); s != nil {
		_ = deps.Client.Detach(s)
	}
	deps.Session.Clear()

	logger.Info("force_detach: session cleared")
	return toolResult(map[string]bool{"success": true})
}

// HandleGetTargets returns the list of connected debug targets.
func HandleGetTargets(deps *Deps, _ map[string]interface{}) *mcp.CallToolResult {
	s, err := deps.Session.Require()
	if err != nil {
		return errResult(err.Error())
	}

	targets, err := deps.Client.GetTargets(s)
	if err != nil {
		return errResult(fmt.Sprintf("Failed to get targets: %v", err))
	}

	// Build response
	type targetJSON struct {
		TargetIDStr string      `json:"targetIDStr"`
		TargetID    interface{} `json:"targetID"`
		TargetType  string      `json:"targetType"`
		State       string      `json:"state"`
		StateNum    int         `json:"stateNum"`
	}
	var result []targetJSON
	for _, t := range targets {
		result = append(result, targetJSON{
			TargetIDStr: t.TargetIDStr,
			TargetID: map[string]interface{}{
				"id":         t.TargetID.ID,
				"targetType": t.TargetType,
			},
			TargetType: t.TargetType,
			State:      t.State,
			StateNum:   t.StateNum,
		})
	}

	metaStatus := map[string]interface{}{}
	if deps.Metadata.IsReady() {
		metaStatus["ready"] = true
		metaStatus["moduleCount"] = deps.Metadata.ModuleCount()
	} else {
		metaStatus["ready"] = false
		metaStatus["message"] = "Metadata is still loading in background..."
	}

	return toolResult(map[string]interface{}{
		"targets":  result,
		"metadata": metaStatus,
	})
}

// HandleSetBreakpoints sets breakpoints in a BSL module.
func HandleSetBreakpoints(deps *Deps, args map[string]interface{}) *mcp.CallToolResult {
	s, err := deps.Session.Require()
	if err != nil {
		return errResult(err.Error())
	}

	moduleName := strArg(args, "moduleName")
	if moduleName == "" {
		return errResult("moduleName is required")
	}
	moduleType := strArg(args, "moduleType")
	if moduleType == "" {
		moduleType = "CommonModule"
	}
	objectID := strArg(args, "objectID")
	extensionName := strArg(args, "extensionName")
	targetIDStr := strArg(args, "targetId")

	// Parse lines
	var lines []int
	if v, ok := args["lines"]; ok {
		if arr, ok := v.([]interface{}); ok {
			for _, item := range arr {
				if n, ok := item.(float64); ok {
					lines = append(lines, int(n))
				}
			}
		}
	}
	if len(lines) == 0 {
		return errResult("At least one line number is required")
	}

	// Auto-resolve objectID from metadata
	if objectID == "" && deps.Metadata.IsReady() {
		prefix := xmlproto.ModuleTypePrefix[moduleType]
		if prefix == "" {
			prefix = moduleType
		}
		if id, ok := deps.Metadata.ResolveObjectID(prefix+"."+moduleName, extensionName); ok {
			objectID = id
			logger.Debug("breakpoints: auto-resolved objectID for %s: %s", moduleName, objectID)
		} else if id, ok := deps.Metadata.ResolveObjectID(moduleName, extensionName); ok {
			objectID = id
		}
	}

	// Build module type string
	moduleTypeStr := buildModuleType(moduleType, moduleName)

	// Build URL
	moduleURL := fmt.Sprintf("e1cib/data/%s", moduleTypeStr)

	propertyID := ""
	if objectID != "" {
		propertyID = xmlproto.ModulePropertyID[moduleType]
	}
	extName := ""
	if objectID != "" {
		extName = deps.Metadata.ResolveExtension(objectID)
	}

	bp := &xmlproto.BPWorkspace{
		Objects: []xmlproto.BPObject{
			{
				ModuleID: xmlproto.ModuleID{
					Type:          moduleTypeStr,
					Name:          moduleName,
					URL:           moduleURL,
					ObjectID:      objectID,
					PropertyID:    propertyID,
					ExtensionName: extName,
				},
				Lines: lines,
			},
		},
	}

	var tid *xmlproto.TargetID
	if targetIDStr != "" {
		tid = &xmlproto.TargetID{ID: targetIDStr}
	}

	if err := deps.Client.SetBreakpoints(s, bp, tid); err != nil {
		return errResult(fmt.Sprintf("Failed to set breakpoints: %v", err))
	}

	deps.Session.SetLastBreakpoints(bp)

	return toolResult(map[string]interface{}{
		"success":    true,
		"moduleName": moduleName,
		"lines":      lines,
	})
}

// HandleClearBreakpoints removes all breakpoints.
func HandleClearBreakpoints(deps *Deps, _ map[string]interface{}) *mcp.CallToolResult {
	s, err := deps.Session.Require()
	if err != nil {
		return errResult(err.Error())
	}

	if err := deps.Client.SetBreakpoints(s, &xmlproto.BPWorkspace{}, nil); err != nil {
		return errResult(fmt.Sprintf("Failed to clear breakpoints: %v", err))
	}
	deps.Session.SetLastBreakpoints(nil)

	return toolResult(map[string]bool{"success": true})
}

// HandleContinue continues execution of a stopped debug target.
func HandleContinue(deps *Deps, args map[string]interface{}) *mcp.CallToolResult {
	return handleStep(deps, args, "Continue")
}

// HandleStepIn steps into the next statement.
func HandleStepIn(deps *Deps, args map[string]interface{}) *mcp.CallToolResult {
	return handleStep(deps, args, "StepIn")
}

// HandleStepOut steps out of the current procedure.
func HandleStepOut(deps *Deps, args map[string]interface{}) *mcp.CallToolResult {
	return handleStep(deps, args, "StepOut")
}

// HandlePause pauses execution on the next statement.
// Uses initSettings(breakOnNextLine=true) — stops on the next executed BSL line.
func HandlePause(deps *Deps, args map[string]interface{}) *mcp.CallToolResult {
	s, err := deps.Session.Require()
	if err != nil {
		return errResult(err.Error())
	}

	// Clear pending stop events so wait_for_stop won't return stale data
	deps.Queue.ClearPendingStop()

	// breakOnNextLine=true stops on the next executed line in any target
	if err := deps.Client.InitSettings(s, true); err != nil {
		return errResult(fmt.Sprintf("Pause failed: %v", err))
	}

	return toolResult(map[string]bool{"success": true})
}

func handleStep(deps *Deps, args map[string]interface{}, action string) *mcp.CallToolResult {
	s, err := deps.Session.Require()
	if err != nil {
		return errResult(err.Error())
	}

	targetIDStr := strArg(args, "targetId")
	if targetIDStr == "" {
		return errResult("targetId is required")
	}

	tid := xmlproto.TargetID{ID: targetIDStr}
	if err := deps.Client.Step(s, tid, action); err != nil {
		return errResult(fmt.Sprintf("Step failed: %v", err))
	}

	return toolResult(map[string]string{"success": "true", "action": action})
}

// HandleWaitForStop waits for a debug target to stop.
func HandleWaitForStop(deps *Deps, args map[string]interface{}) *mcp.CallToolResult {
	_, err := deps.Session.Require()
	if err != nil {
		return errResult(err.Error())
	}

	timeoutMs := intArg(args, "timeout", 30000)
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutMs)*time.Millisecond)
	defer cancel()

	stopEvent, err := deps.Queue.WaitForStop(ctx)
	if err != nil {
		return errResult(fmt.Sprintf("Timeout: %v", err))
	}

	// Reset breakOnNextLine after stop — so next execution doesn't stop on every line
	if s, serr := deps.Session.Require(); serr == nil {
		_ = deps.Client.InitSettings(s, false)
	}

	// Resolve module name from callStack
	moduleName := stopEvent.ModuleName
	if moduleName == "" && len(stopEvent.CallStack) > 0 {
		frame := stopEvent.CallStack[0]
		if frame.ModuleID.ObjectID != "" {
			if name, ok := deps.Metadata.ResolveName(frame.ModuleID.ObjectID); ok {
				moduleName = name
			}
		}
		if moduleName == "" {
			moduleName = frame.ModuleID.Type
		}
	}

	// Build callStack response
	type frameJSON struct {
		ModuleID interface{} `json:"moduleID"`
		LineNo   int         `json:"lineNo"`
	}
	var callStack []frameJSON
	for _, f := range stopEvent.CallStack {
		name := f.ModuleID.Type
		if f.ModuleID.ObjectID != "" {
			if n, ok := deps.Metadata.ResolveName(f.ModuleID.ObjectID); ok {
				name = n
			}
		}
		if name == "" {
			name = f.ModuleID.URL
		}
		moduleType := f.ModuleID.Type
		if moduleType == "" && f.ModuleID.PropertyID != "" {
			moduleType = xmlproto.PropertyIDToModuleType[f.ModuleID.PropertyID]
		}
		callStack = append(callStack, frameJSON{
			ModuleID: map[string]string{
				"type":          moduleType,
				"name":          name,
				"url":           f.ModuleID.URL,
				"objectID":      f.ModuleID.ObjectID,
				"propertyID":    f.ModuleID.PropertyID,
				"extensionName": f.ModuleID.ExtensionName,
			},
			LineNo: f.LineNo,
		})
	}

	return toolResult(map[string]interface{}{
		"targetId":   stopEvent.TargetID,
		"moduleName": moduleName,
		"lineNo":     stopEvent.LineNo,
		"callStack":  callStack,
	})
}

// HandleGetVariables retrieves local variables of a stopped debug target.
func HandleGetVariables(deps *Deps, args map[string]interface{}) *mcp.CallToolResult {
	s, err := deps.Session.Require()
	if err != nil {
		return errResult(err.Error())
	}

	targetIDStr := strArg(args, "targetId")
	if targetIDStr == "" {
		return errResult("targetId is required")
	}

	tid := xmlproto.TargetID{ID: targetIDStr}
	results, err := deps.Client.EvalLocalVariables(s, tid, deps.Queue)
	if err != nil {
		return errResult(fmt.Sprintf("Failed to get variables: %v", err))
	}

	type varJSON struct {
		Name     string `json:"name"`
		TypeName string `json:"typeName"`
		Value    string `json:"value"`
	}
	var variables []varJSON
	for _, r := range results {
		variables = append(variables, varJSON{
			Name:     r.Name,
			TypeName: r.TypeName,
			Value:    r.Value,
		})
	}

	return toolResult(map[string]interface{}{"variables": variables})
}

// HandleEvaluate evaluates a BSL expression in the context of a stopped target.
func HandleEvaluate(deps *Deps, args map[string]interface{}) *mcp.CallToolResult {
	s, err := deps.Session.Require()
	if err != nil {
		return errResult(err.Error())
	}

	targetIDStr := strArg(args, "targetId")
	if targetIDStr == "" {
		return errResult("targetId is required")
	}
	expression := strArg(args, "expression")
	if expression == "" {
		return errResult("expression is required")
	}

	tid := xmlproto.TargetID{ID: targetIDStr}
	result, err := deps.Client.EvalExpr(s, tid, expression, deps.Queue)
	if err != nil {
		return errResult(fmt.Sprintf("Failed to evaluate: %v", err))
	}

	return toolResult(map[string]interface{}{
		"expression": expression,
		"result": map[string]string{
			"typeName": result.TypeName,
			"value":    result.Value,
		},
	})
}

// HandleRawRequest sends an arbitrary XML request to the debug server.
func HandleRawRequest(deps *Deps, args map[string]interface{}) *mcp.CallToolResult {
	cmd := strArg(args, "cmd")
	if cmd == "" {
		return errResult("cmd is required")
	}
	xmlBody := strArg(args, "xml")
	if xmlBody == "" {
		return errResult("xml is required")
	}
	dbgui := strArg(args, "dbgui")

	url := deps.Config.URL
	if s := deps.Session.Get(); s != nil {
		url = s.URL
	}
	if url == "" {
		url = "http://localhost:1550"
	}

	body, status, err := deps.Client.RawRequest(url, cmd, xmlBody, dbgui)
	if err != nil {
		return errResult(fmt.Sprintf("Request failed: %v", err))
	}

	return toolResult(map[string]interface{}{
		"status": status,
		"body":   body,
	})
}

// HandleGetCallStack returns the call stack from the last stop event.
func HandleGetCallStack(deps *Deps, args map[string]interface{}) *mcp.CallToolResult {
	_, err := deps.Session.Require()
	if err != nil {
		return errResult(err.Error())
	}

	targetIDStr := strArg(args, "targetId")

	lastStop := deps.Queue.GetLastCallStack()
	if lastStop == nil || (targetIDStr != "" && lastStop.TargetID != targetIDStr) {
		return toolResult(map[string]interface{}{
			"callStack": []interface{}{},
			"note":      "No call stack available — target not stopped or targetId mismatch",
		})
	}

	type frameJSON struct {
		ModuleID interface{} `json:"moduleID"`
		LineNo   int         `json:"lineNo"`
	}
	var callStack []frameJSON
	topModuleName := ""
	for _, f := range lastStop.CallStack {
		name := f.ModuleID.Type
		if f.ModuleID.ObjectID != "" {
			if n, ok := deps.Metadata.ResolveName(f.ModuleID.ObjectID); ok {
				name = n
			}
		}
		if name == "" {
			name = f.ModuleID.URL
		}
		// Determine module type from propertyID if type is empty
		moduleType := f.ModuleID.Type
		if moduleType == "" && f.ModuleID.PropertyID != "" {
			moduleType = xmlproto.PropertyIDToModuleType[f.ModuleID.PropertyID]
		}
		if topModuleName == "" {
			topModuleName = name
		}
		callStack = append(callStack, frameJSON{
			ModuleID: map[string]string{
				"type":          moduleType,
				"name":          name,
				"url":           f.ModuleID.URL,
				"objectID":      f.ModuleID.ObjectID,
				"propertyID":    f.ModuleID.PropertyID,
				"extensionName": f.ModuleID.ExtensionName,
			},
			LineNo: f.LineNo,
		})
	}

	moduleName := lastStop.ModuleName
	if moduleName == "" {
		moduleName = topModuleName
	}

	return toolResult(map[string]interface{}{
		"targetId":   lastStop.TargetID,
		"moduleName": moduleName,
		"lineNo":     lastStop.LineNo,
		"callStack":  callStack,
	})
}
func HandleReloadMetadata(deps *Deps, args map[string]interface{}) *mcp.CallToolResult {
	skipCache := false
	if val, ok := args["skipCache"].(bool); ok {
		skipCache = val
	}

	count, err := deps.Metadata.Reload(skipCache)
	if err != nil {
		return errResult(fmt.Sprintf("Failed to reload metadata: %v", err))
	}

	msg := fmt.Sprintf("Reloaded %d modules", count)
	if skipCache {
		msg += " (cache bypassed)"
	}

	return toolResult(map[string]interface{}{
		"success":     true,
		"moduleCount": count,
		"skipCache":   skipCache,
		"message":     msg,
	})
}

// buildModuleType builds the composite module type string expected by 1C platform.
func buildModuleType(moduleType, moduleName string) string {
	if moduleType == "CommonModule" {
		return moduleName
	}
	prefix := xmlproto.ModuleTypePrefix[moduleType]
	if prefix != "" {
		return prefix + "." + moduleName
	}
	return moduleType + "." + moduleName
}

// splitPaths splits a comma or semicolon separated path string.
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
