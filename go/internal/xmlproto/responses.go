package xmlproto

import (
	"encoding/xml"
	"regexp"
	"strings"
)

// StripNamespacePrefixes removes XML namespace prefixes from element names
// so standard encoding/xml can parse them without namespace awareness.
func StripNamespacePrefixes(s string) string {
	// Remove namespace declarations
	re := regexp.MustCompile(`\s+xmlns(?::\w+)?="[^"]*"`)
	s = re.ReplaceAllString(s, "")
	// Remove namespace prefixes from tags: <ns:Tag> → <Tag>, </ns:Tag> → </Tag>
	re2 := regexp.MustCompile(`(</?)\w+:`)
	s = re2.ReplaceAllString(s, "$1")
	return s
}

// exceptionDescrRe extracts the <descr> text of a platform exception document,
// which is what the debug server returns instead of a response on HTTP 4xx.
var exceptionDescrRe = regexp.MustCompile(`(?s)<(?:\w+:)?descr>(.*?)</(?:\w+:)?descr>`)

// ExceptionDescr returns the human-readable description of a platform exception
// response, or a trimmed fragment of the body when it is not one.
func ExceptionDescr(body string) string {
	if m := exceptionDescrRe.FindStringSubmatch(body); len(m) == 2 {
		return strings.Join(strings.Fields(m[1]), " ")
	}
	body = strings.TrimSpace(body)
	if len(body) > 200 {
		return body[:200] + "…"
	}
	return body
}

// --- Attach response ---

type AttachResponse struct {
	XMLName xml.Name `xml:"response"`
	Result  string   `xml:"result"`
}

// --- Ping response ---

type PingResponse struct {
	XMLName xml.Name    `xml:"response"`
	Items   []PingEvent `xml:"result"`
}

type PingEvent struct {
	CmdID      string         `xml:"cmdID"`
	TargetID   PingTargetID   `xml:"targetID"`
	CallStack  []PingFrame    `xml:"callStack"`
	ExprResult *PingEvalData  `xml:"evalExprResBaseData"`
}

type PingTargetID struct {
	ID string `xml:"id"`
}

type PingFrame struct {
	ModuleID PingModuleID `xml:"moduleID"`
	LineNo   int          `xml:"lineNo"`
}

type PingModuleID struct {
	Type          string `xml:"type"`
	URL           string `xml:"URL"`
	ObjectID      string `xml:"objectID"`
	PropertyID    string `xml:"propertyID"`
	ExtensionName string `xml:"extensionName"`
}

type PingEvalData struct {
	ExpressionResultID string              `xml:"expressionResultID"`
	ResultValueInfo    PingEvalItem        `xml:"resultValueInfo"`
	LocalVariableName  string              `xml:"localVariableName"`
	CalculationResult  *PingCalcResult     `xml:"calculationResult"`
}

type PingCalcResult struct {
	ValueOfContextPropInfo []PingContextPropInfo `xml:"valueOfContextPropInfo"`
}

type PingContextPropInfo struct {
	PropInfo  PingPropInfo  `xml:"propInfo"`
	ValueInfo PingEvalItem  `xml:"valueInfo"`
}

type PingPropInfo struct {
	PropName  string `xml:"propName"`
	IsReaded  bool   `xml:"isReaded"`
	ErrorStr  string `xml:"errorStr"`
}

type PingEvalItem struct {
	TypeName          string `xml:"typeName"`
	Pres              string `xml:"pres"`
	Name              string `xml:"name"`
	LocalVariableName string `xml:"localVariableName"`
}

// --- GetTargets response ---

type GetTargetsResponse struct {
	XMLName xml.Name      `xml:"response"`
	Result  string        `xml:"result"`
	Items   []TargetState `xml:"item"`
}

type TargetState struct {
	TargetIDStr string         `xml:"targetIDStr"`
	TargetID    ResponseTargetID `xml:"targetID"`
	StateNum    int            `xml:"stateNum"`
	State       string         `xml:"state"`
}

type ResponseTargetID struct {
	ID              string `xml:"id"`
	SeanceID        string `xml:"seanceId"`
	SeanceNo        int    `xml:"seanceNo"`
	InfoBaseAlias   string `xml:"infoBaseAlias"`
	TargetType      string `xml:"targetType"`
	UserName        string `xml:"userName"`
	ConfigVersion   string `xml:"configVersion"`
}

// --- CallStack response ---

type CallStackResponse struct {
	XMLName   xml.Name         `xml:"response"`
	Result    string           `xml:"result"`
	CallStack []CallStackFrame `xml:"callStack"`
}

type CallStackFrame struct {
	ModuleID CallStackModuleID `xml:"moduleID"`
	LineNo   int               `xml:"lineNo"`
}

type CallStackModuleID struct {
	Type          string `xml:"type"`
	URL           string `xml:"URL"`
	ObjectID      string `xml:"objectID"`
	PropertyID    string `xml:"propertyID"`
	ExtensionName string `xml:"extensionName"`
}

// ParsePingResponse parses a ping XML response after stripping namespaces.
func ParsePingResponse(body string) (*PingResponse, error) {
	stripped := StripNamespacePrefixes(body)
	var resp PingResponse
	if err := xml.Unmarshal([]byte(stripped), &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// ParseGetTargetsResponse parses a getDbgAllTargetStates XML response.
func ParseGetTargetsResponse(body string) (*GetTargetsResponse, error) {
	stripped := StripNamespacePrefixes(body)
	var resp GetTargetsResponse
	if err := xml.Unmarshal([]byte(stripped), &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// ParseCallStackResponse parses a getCallStack XML response.
func ParseCallStackResponse(body string) (*CallStackResponse, error) {
	stripped := StripNamespacePrefixes(body)
	var resp CallStackResponse
	if err := xml.Unmarshal([]byte(stripped), &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// ParseAttachResponse parses an attach XML response.
func ParseAttachResponse(body string) (*AttachResponse, error) {
	stripped := StripNamespacePrefixes(body)
	var resp AttachResponse
	if err := xml.Unmarshal([]byte(stripped), &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// EvalResultItem represents a single result item from evalLocalVariables or evalExpr direct HTTP response.
type EvalResultItem struct {
	ExpressionResultID string `xml:"expressionResultID"`
	LocalVariableName  string `xml:"localVariableName"`
	ResultValueInfo    struct {
		TypeName string `xml:"typeName"`
		Pres     string `xml:"pres"`
	} `xml:"resultValueInfo"`
	ErrorOccurred bool `xml:"errorOccurred"`
}

// EvalLocalVariablesResponse is the direct HTTP response from evalLocalVariables.
type EvalLocalVariablesResponse struct {
	XMLName xml.Name       `xml:"response"`
	Result  EvalResultItem `xml:"result"`
}

// EvalExprResponse is the direct HTTP response from evalExpr.
type EvalExprResponse struct {
	XMLName xml.Name         `xml:"response"`
	Results []EvalResultItem `xml:"result"`
}

// ParseEvalLocalVariablesResponse parses a direct evalLocalVariables HTTP response.
func ParseEvalLocalVariablesResponse(body string) (*EvalLocalVariablesResponse, error) {
	stripped := StripNamespacePrefixes(body)
	var resp EvalLocalVariablesResponse
	if err := xml.Unmarshal([]byte(stripped), &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// ParseEvalExprResponse parses a direct evalExpr HTTP response.
func ParseEvalExprResponse(body string) (*EvalExprResponse, error) {
	stripped := StripNamespacePrefixes(body)
	var resp EvalExprResponse
	if err := xml.Unmarshal([]byte(stripped), &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// ExtractResult extracts the <result> field from any XML response body.
func ExtractResult(body string) string {
	stripped := StripNamespacePrefixes(body)
	start := strings.Index(stripped, "<result>")
	end := strings.Index(stripped, "</result>")
	if start == -1 || end == -1 {
		return ""
	}
	return stripped[start+8 : end]
}
