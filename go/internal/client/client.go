package client

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/1c-debug-mcp/go/internal/events"
	"github.com/1c-debug-mcp/go/internal/logger"
	"github.com/1c-debug-mcp/go/internal/session"
	"github.com/1c-debug-mcp/go/internal/xmlproto"
	"github.com/google/uuid"
)

// DebugTarget represents a connected 1C debug target (process).
type DebugTarget struct {
	TargetIDStr string
	TargetID    xmlproto.TargetID
	TargetType  string
	State       string
	StateNum    int
}

// DebugEventType represents the type of a debug event from ping.
type DebugEventType string

const (
	EventCallStackFormed DebugEventType = "callStackFormed"
	EventExprEvaluated   DebugEventType = "exprEvaluated"
	EventTargetStarted   DebugEventType = "targetStarted"
	EventTargetQuit      DebugEventType = "targetQuit"
)

// DebugEvent represents a parsed event from pingDebugUIParams.
type DebugEvent struct {
	Type               DebugEventType
	TargetID           string
	TargetIDStr        string
	CallStack          []xmlproto.StackFrame
	ExpressionResultID string
	EvalItems          []EvalItem
}

// EvalItem represents a single variable/expression result.
type EvalItem struct {
	Name              string
	LocalVariableName string
	TypeName          string
	Pres              string // base64-encoded presentation
}

// Client is the HTTP client for the 1C debug server (dbgs.exe).
type Client struct {
	http *http.Client
	// compactBPInfo is set once the platform has rejected the extended <bpInfo>
	// element set, so subsequent setBreakpoints calls skip the failing attempt.
	compactBPInfo atomic.Bool
}

// New creates a new Client.
func New() *Client {
	return &Client{
		http: &http.Client{Timeout: 30 * time.Second},
	}
}

// post sends a POST request to the debug server.
func (c *Client) post(url, cmd, xmlBody string) (string, int, error) {
	reqURL := fmt.Sprintf("%s/e1crdbg/rdbg?cmd=%s", url, cmd)
	logger.Debug("POST %s body: %.400s", cmd, xmlBody)
	req, err := http.NewRequest("POST", reqURL, strings.NewReader(xmlBody))
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("Content-Type", "application/xml; charset=utf-8")
	req.Header.Set("Accept", "application/xml")
	req.Header.Set("User-Agent", "1CV8")

	resp, err := c.http.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", resp.StatusCode, err
	}
	logger.Debug("POST %s response [%d]: %.400s", cmd, resp.StatusCode, string(body))
	return string(body), resp.StatusCode, nil
}

// Test verifies connectivity to the debug server.
func (c *Client) Test(url string) error {
	// Use rdbgTest endpoint with cmd=test (as per onec-debug-adapter)
	reqURL := fmt.Sprintf("%s/e1crdbg/rdbgTest?cmd=test", url)
	req, err := http.NewRequest("POST", reqURL, strings.NewReader(""))
	if err != nil {
		return fmt.Errorf("debug server unreachable at %s: %w", url, err)
	}
	req.Header.Set("Content-Type", "application/xml; charset=utf-8")
	req.Header.Set("Accept", "application/xml")
	req.Header.Set("User-Agent", "1CV8")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("debug server unreachable at %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 500 {
		return fmt.Errorf("debug server error at %s: HTTP %d", url, resp.StatusCode)
	}
	return nil
}

// Attach connects to the debug server and sets up the session.
func (c *Client) Attach(s *session.Session) error {
	logger.Info("Attach: alias=%s id=%s", s.Alias, s.ID)
	xmlBody := xmlproto.BuildAttachXML(s.Alias, s.ID, s.Password)
	body, _, err := c.post(s.URL, "attachDebugUI", xmlBody)
	if err != nil {
		return err
	}

	result := xmlproto.ExtractResult(body)
	switch result {
	case "ibInDebug":
		return fmt.Errorf("ibInDebug")
	case "notRegistered":
		return fmt.Errorf("notRegistered — check infobaseAlias: %s", s.Alias)
	case "credentialsRequired", "fullCredentialsRequired":
		return fmt.Errorf("%s — password required", result)
	}
	return nil
}

// AttachWithRetry attaches with retry on ibInDebug (up to 5 attempts).
func (c *Client) AttachWithRetry(s *session.Session) error {
	for i := 0; i < 5; i++ {
		err := c.Attach(s)
		if err == nil {
			return nil
		}
		if err.Error() == "ibInDebug" {
			// Try to detach first, then retry
			_ = c.Detach(s)
			time.Sleep(1 * time.Second)
			continue
		}
		return err
	}
	return fmt.Errorf("ibInDebug — another debugger is connected, failed after 5 retries")
}

// Detach disconnects from the debug server.
func (c *Client) Detach(s *session.Session) error {
	xmlBody := xmlproto.BuildDetachXML(s.Alias, s.ID)
	_, _, err := c.post(s.URL, "detachDebugUI", xmlBody)
	return err
}

// InitSettings sends initial debug settings.
func (c *Client) InitSettings(s *session.Session, breakOnNextLine bool) error {
	xmlBody := xmlproto.BuildInitSettingsXML(s.Alias, s.ID, breakOnNextLine)
	_, _, err := c.post(s.URL, "initSettings", xmlBody)
	return err
}

// SetAutoAttach configures auto-attach for the given target types.
func (c *Client) SetAutoAttach(s *session.Session, targetTypes []string) error {
	xmlBody := xmlproto.BuildAutoAttachXML(s.Alias, s.ID, targetTypes)
	_, _, err := c.post(s.URL, "setAutoAttachSettings", xmlBody)
	return err
}

// Ping polls the debug server for events.
// Note: ping sends NO body and requires dbgui query parameter.
func (c *Client) Ping(s *session.Session) ([]DebugEvent, int, error) {
	reqURL := fmt.Sprintf("%s/e1crdbg/rdbg?cmd=pingDebugUIParams&dbgui=%s", s.URL, s.ID)
	req, err := http.NewRequest("POST", reqURL, strings.NewReader(""))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/xml; charset=utf-8")
	req.Header.Set("Accept", "application/xml")
	req.Header.Set("User-Agent", "1CV8")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, err
	}
	status := resp.StatusCode

	if status == 400 {
		return nil, status, fmt.Errorf("HTTP 400 from ping")
	}

	bodyStr := string(body)
	if strings.TrimSpace(bodyStr) == "" {
		return nil, status, nil
	}

	pingResp, err := xmlproto.ParsePingResponse(bodyStr)
	if err != nil {
		return nil, status, err
	}

	var events []DebugEvent
	for _, item := range pingResp.Items {
		ev := DebugEvent{
			TargetID: item.TargetID.ID,
		}
		switch item.CmdID {
		case "callStackFormed":
			ev.Type = EventCallStackFormed
			for _, f := range item.CallStack {
				ev.CallStack = append(ev.CallStack, xmlproto.StackFrame{
					ModuleID: xmlproto.ModuleID{
						Type:          f.ModuleID.Type,
						URL:           f.ModuleID.URL,
						ObjectID:      f.ModuleID.ObjectID,
						PropertyID:    f.ModuleID.PropertyID,
						ExtensionName: f.ModuleID.ExtensionName,
					},
					LineNo: f.LineNo,
				})
			}
		case "exprEvaluated":
			ev.Type = EventExprEvaluated
			if item.ExprResult != nil {
				ev.ExpressionResultID = item.ExprResult.ExpressionResultID
				// evalLocalVariables: variables in calculationResult.valueOfContextPropInfo
				if item.ExprResult.CalculationResult != nil {
					for _, prop := range item.ExprResult.CalculationResult.ValueOfContextPropInfo {
						ev.EvalItems = append(ev.EvalItems, EvalItem{
							Name:     prop.PropInfo.PropName,
							TypeName: prop.ValueInfo.TypeName,
							Pres:     prop.ValueInfo.Pres,
						})
					}
				}
				// evalExpr: single result in resultValueInfo
				if len(ev.EvalItems) == 0 && item.ExprResult.ResultValueInfo.TypeName != "" {
					ev.EvalItems = append(ev.EvalItems, EvalItem{
						Name:              item.ExprResult.LocalVariableName,
						LocalVariableName: item.ExprResult.LocalVariableName,
						TypeName:          item.ExprResult.ResultValueInfo.TypeName,
						Pres:              item.ExprResult.ResultValueInfo.Pres,
					})
				}
			}
		case "targetStarted", "DBGUIExtCmdInfoStarted":
			ev.Type = EventTargetStarted
		case "targetQuit", "DBGUIExtCmdInfoQuit":
			ev.Type = EventTargetQuit
		}
		if ev.Type != "" {
			events = append(events, ev)
		}
	}
	return events, status, nil
}

// GetTargets returns the list of connected debug targets.
func (c *Client) GetTargets(s *session.Session) ([]DebugTarget, error) {
	xmlBody := xmlproto.BuildGetTargetsXML(s.Alias, s.ID)
	body, _, err := c.post(s.URL, "getDbgAllTargetStates", xmlBody)
	if err != nil {
		return nil, err
	}

	resp, err := xmlproto.ParseGetTargetsResponse(body)
	if err != nil {
		return nil, err
	}

	var targets []DebugTarget
	for _, item := range resp.Items {
		targets = append(targets, DebugTarget{
			TargetIDStr: item.TargetIDStr,
			TargetID: xmlproto.TargetID{
				ID: item.TargetID.ID,
			},
			TargetType: item.TargetID.TargetType,
			State:      item.State,
			StateNum:   item.StateNum,
		})
	}
	return targets, nil
}

// SetBreakpoints sets breakpoints in a BSL module.
func (c *Client) SetBreakpoints(s *session.Session, bp *xmlproto.BPWorkspace, targetID *xmlproto.TargetID) error {
	if targetID != nil {
		_ = c.ClearBreakOnNextStatement(s)
		_ = c.AttachDetachTargets(s, []xmlproto.TargetID{*targetID}, true)
	}
	compact := c.compactBPInfo.Load()
	body, status, err := c.post(s.URL, "setBreakpoints", xmlproto.BuildSetBreakpointsXML(s.Alias, s.ID, bp, compact))
	if err != nil {
		return err
	}
	if status == http.StatusOK {
		return nil
	}

	// Platforms before 8.3.24 don't know the extended <bpInfo> properties and
	// answer 400 with an XDTO conversion error. Retry once with the compact
	// element set and remember the choice for the whole session.
	if !compact && status == http.StatusBadRequest {
		logger.Info("setBreakpoints: platform rejected extended bpInfo (%s), retrying with compact format",
			xmlproto.ExceptionDescr(body))
		body, status, err = c.post(s.URL, "setBreakpoints", xmlproto.BuildSetBreakpointsXML(s.Alias, s.ID, bp, true))
		if err != nil {
			return err
		}
		if status == http.StatusOK {
			c.compactBPInfo.Store(true)
			logger.Info("setBreakpoints: compact bpInfo accepted, using it from now on")
			return nil
		}
	}

	return fmt.Errorf("setBreakpoints failed: HTTP %d: %s", status, xmlproto.ExceptionDescr(body))
}

// AttachDetachTargets attaches or detaches a list of debug targets.
// Always calls clearBreakOnNextStatement before attaching (as per onec-debug-adapter).
func (c *Client) AttachDetachTargets(s *session.Session, targetIDs []xmlproto.TargetID, attach bool) error {
	if attach {
		_ = c.ClearBreakOnNextStatement(s)
	}
	xmlBody := xmlproto.BuildAttachDetachTargetsXML(s.Alias, s.ID, targetIDs, attach)
	_, _, err := c.post(s.URL, "attachDetachDbgTargets", xmlBody)
	return err
}

// AttachDetachTargetsNoClear attaches targets WITHOUT calling clearBreakOnNextStatement first.
// Used for pause — clearing would reset the break-on-next-line flag.
func (c *Client) AttachDetachTargetsNoClear(s *session.Session, targetIDs []xmlproto.TargetID, attach bool) error {
	xmlBody := xmlproto.BuildAttachDetachTargetsXML(s.Alias, s.ID, targetIDs, attach)
	_, _, err := c.post(s.URL, "attachDetachDbgTargets", xmlBody)
	return err
}

// ClearBreakOnNextStatement clears the break-on-next-statement flag.
func (c *Client) ClearBreakOnNextStatement(s *session.Session) error {
	xmlBody := xmlproto.BuildClearBreakOnNextStatementXML(s.Alias, s.ID)
	_, _, err := c.post(s.URL, "clearBreakOnNextStatement", xmlBody)
	return err
}

// GetCallStack returns the call stack for a stopped debug target.
func (c *Client) GetCallStack(s *session.Session, targetID xmlproto.TargetID) ([]xmlproto.StackFrame, error) {
	xmlBody := xmlproto.BuildGetCallStackXML(s.Alias, s.ID, targetID)
	body, _, err := c.post(s.URL, "getCallStack", xmlBody)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(body) == "" {
		return nil, nil
	}
	resp, err := xmlproto.ParseCallStackResponse(body)
	if err != nil {
		return nil, err
	}
	var frames []xmlproto.StackFrame
	for _, f := range resp.CallStack {
		frames = append(frames, xmlproto.StackFrame{
			ModuleID: xmlproto.ModuleID{
				Type:          f.ModuleID.Type,
				URL:           f.ModuleID.URL,
				ObjectID:      f.ModuleID.ObjectID,
				PropertyID:    f.ModuleID.PropertyID,
				ExtensionName: f.ModuleID.ExtensionName,
			},
			LineNo: f.LineNo,
		})
	}
	return frames, nil
}

// Step sends a step command (continue, stepIn, stepOut, breakOnNextStatement).
func (c *Client) Step(s *session.Session, targetID xmlproto.TargetID, action string) error {
	xmlBody := xmlproto.BuildStepXML(s.Alias, s.ID, targetID, action)
	_, _, err := c.post(s.URL, "step", xmlBody)
	return err
}

// EvalLocalVariables retrieves local variables for a stopped target.
// Tries to use the direct HTTP response first; falls back to waiting for a ping event.
func (c *Client) EvalLocalVariables(s *session.Session, targetID xmlproto.TargetID, queue *events.Queue) ([]events.EvalItem, error) {
	resultID := uuid.New().String()
	ch := queue.RegisterEvalWaiter(resultID)

	xmlBody := xmlproto.BuildEvalLocalVariablesXML(s.Alias, s.ID, targetID, resultID)
	body, _, err := c.post(s.URL, "evalLocalVariables", xmlBody)
	if err != nil {
		return nil, err
	}

	// Try to parse direct HTTP response first
	if resp, err := xmlproto.ParseEvalLocalVariablesResponse(body); err == nil &&
		resp.Result.ExpressionResultID != "" {
		// Direct response received — deliver to queue and return
		items := directEvalResultToItems(resp.Result)
		result := events.EvalResult{Items: items}
		if len(items) > 0 {
			result.TypeName = items[0].TypeName
			result.Value = items[0].Value
		}
		queue.DeliverEval(resultID, result)
		return items, nil
	}

	// Fall back to waiting for ping event
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	select {
	case result := <-ch:
		return result.Items, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("Timeout waiting for evalLocalVariables result")
	}
}

// EvalExpr evaluates a BSL expression in the context of a stopped target.
// Tries to use the direct HTTP response first; falls back to waiting for a ping event.
func (c *Client) EvalExpr(s *session.Session, targetID xmlproto.TargetID, expression string, queue *events.Queue) (*events.EvalResult, error) {
	resultID := uuid.New().String()
	ch := queue.RegisterEvalWaiter(resultID)

	xmlBody := xmlproto.BuildEvalExprXML(s.Alias, s.ID, targetID, expression, resultID)
	body, _, err := c.post(s.URL, "evalExpr", xmlBody)
	if err != nil {
		return nil, err
	}

	// Try to parse direct HTTP response first
	if resp, err := xmlproto.ParseEvalExprResponse(body); err == nil && len(resp.Results) > 0 {
		item := resp.Results[0]
		if item.ExpressionResultID != "" {
			value := DecodePresentation(item.ResultValueInfo.Pres)
			result := &events.EvalResult{
				TypeName: item.ResultValueInfo.TypeName,
				Value:    value,
			}
			queue.DeliverEval(resultID, *result)
			return result, nil
		}
	}

	// Fall back to waiting for ping event
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	select {
	case result := <-ch:
		return &result, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("Timeout waiting for evalExpr result")
	}
}

// directEvalResultToItems converts a direct HTTP eval result to EvalItem slice.
func directEvalResultToItems(r xmlproto.EvalResultItem) []events.EvalItem {
	name := r.LocalVariableName
	value := DecodePresentation(r.ResultValueInfo.Pres)
	return []events.EvalItem{
		{
			Name:     name,
			TypeName: r.ResultValueInfo.TypeName,
			Value:    value,
		},
	}
}

// RawRequest sends an arbitrary XML request to the debug server.
func (c *Client) RawRequest(url, cmd, xmlBody, dbgui string) (string, int, error) {
	reqURL := fmt.Sprintf("%s/e1crdbg/rdbg?cmd=%s", url, cmd)
	if dbgui != "" {
		reqURL += "&dbgui=" + dbgui
	}
	req, err := http.NewRequest("POST", reqURL, strings.NewReader(xmlBody))
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("Content-Type", "application/xml; charset=utf-8")
	req.Header.Set("Accept", "application/xml")
	req.Header.Set("User-Agent", "1CV8")

	resp, err := c.http.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return string(body), resp.StatusCode, nil
}

// DecodePresentation decodes a base64-encoded presentation string.
func DecodePresentation(pres string) string {
	if pres == "" {
		return ""
	}
	b, err := base64.StdEncoding.DecodeString(pres)
	if err != nil {
		return pres
	}
	return string(b)
}
