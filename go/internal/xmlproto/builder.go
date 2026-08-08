package xmlproto

import (
	"fmt"
	"strings"
)

const xmlHeader = `<?xml version="1.0" encoding="UTF-8"?>`
const ns = `http://v8.1c.ru/8.3/debugger/debugRDBGRequestResponse`
const nsBD = `http://v8.1c.ru/8.3/debugger/debugBaseData`
const nsBP = `http://v8.1c.ru/8.3/debugger/debugBreakpoints`
const nsCalc = `http://v8.1c.ru/8.3/debugger/debugCalculations`
const nsAutoAttach = `http://v8.1c.ru/8.3/debugger/debugAutoAttach`
const nsXSI = `http://www.w3.org/2001/XMLSchema-instance`

// BuildRequestXML builds a basic XML request with the given body.
func BuildRequestXML(alias, id, body string) string {
	return fmt.Sprintf(`%s<request xmlns="%s"><infoBaseAlias>%s</infoBaseAlias><idOfDebuggerUI>%s</idOfDebuggerUI>%s</request>`,
		xmlHeader, ns, EscapeXML(alias), EscapeXML(id), body)
}

// BuildAttachXML builds attachDebugUI request XML.
func BuildAttachXML(alias, id, password string) string {
	var credXml string
	if password != "" {
		credXml = fmt.Sprintf(`<credentials>%s</credentials>`, EscapeXML(password))
	}
	body := fmt.Sprintf(`<options><foregroundAbility>true</foregroundAbility></options>%s`, credXml)
	return BuildRequestXML(alias, id, body)
}

// BuildDetachXML builds detachDebugUI request XML.
func BuildDetachXML(alias, id string) string {
	return BuildRequestXML(alias, id, "")
}

// BuildInitSettingsXML builds initSettings request XML.
func BuildInitSettingsXML(alias, id string, breakOnNextLine bool) string {
	breakVal := "false"
	if breakOnNextLine {
		breakVal = "true"
	}
	body := fmt.Sprintf(`<data><breakOnNextLine>%s</breakOnNextLine></data>`, breakVal)
	return BuildRequestXML(alias, id, body)
}

// BuildAutoAttachXML builds setAutoAttachSettings request XML.
func BuildAutoAttachXML(alias, id string, targetTypes []string) string {
	var sb strings.Builder
	for _, t := range targetTypes {
		sb.WriteString(fmt.Sprintf(`<aa:targetType>%s</aa:targetType>`, EscapeXML(t)))
	}
	body := fmt.Sprintf(
		`<autoAttachSettings xsi:type="aa:DebugAutoAttachSettings" xmlns:xsi="%s" xmlns:aa="%s">%s<aa:areaName></aa:areaName></autoAttachSettings>`,
		nsXSI, nsAutoAttach, sb.String(),
	)
	return BuildRequestXML(alias, id, body)
}

// BuildPingXML builds pingDebugUIParams request XML.
// Note: ping uses only idOfDebuggerUI without infoBaseAlias and without body.
func BuildPingXML(id string) string {
	return fmt.Sprintf(`%s<request xmlns="%s"><idOfDebuggerUI>%s</idOfDebuggerUI></request>`,
		xmlHeader, ns, EscapeXML(id))
}

// BuildGetTargetsXML builds getDbgAllTargetStates request XML.
func BuildGetTargetsXML(alias, id string) string {
	return BuildRequestXML(alias, id, "")
}

// BuildSetBreakpointsXML builds setBreakpoints request XML with three namespaces.
//
// compactBPInfo limits <bpInfo> to <line> and <isActive>. Platforms before
// 8.3.24 don't have the conditional/hit-count properties in their
// debugBreakpoints XDTO package and reject the extended element set with
// "Ошибка преобразования данных XDTO" (HTTP 400), so no breakpoint is set at all.
// Checked against debug servers 8.3.10.2667 … 8.5.1.1150: 8.3.23.2157 and older
// answer 400, 8.3.24.1667 and newer answer 200.
func BuildSetBreakpointsXML(alias, id string, bp *BPWorkspace, compactBPInfo bool) string {
	var modulesXml strings.Builder
	for _, obj := range bp.Objects {
		// Build breakpoint lines XML
		var bpLines strings.Builder
		for _, line := range obj.Lines {
			if compactBPInfo {
				bpLines.WriteString(fmt.Sprintf(
					`<bpInfo xmlns="%s">`+
						`<line>%d</line>`+
						`<isActive>true</isActive>`+
						`</bpInfo>`,
					nsBP, line,
				))
				continue
			}
			bpLines.WriteString(fmt.Sprintf(
				`<bpInfo xmlns="%s">`+
					`<line>%d</line>`+
					`<isActive>true</isActive>`+
					`<breakOnCondition>false</breakOnCondition>`+
					`<condition></condition>`+
					`<breakOnParentMethod>false</breakOnParentMethod>`+
					`<parentMethod></parentMethod>`+
					`<breakOnHitCount>false</breakOnHitCount>`+
					`<hitCountVariant>0</hitCountVariant>`+
					`<hitCount>1</hitCount>`+
					`<temp>false</temp>`+
					`</bpInfo>`,
				nsBP, line,
			))
		}

		// Build moduleID XML — use objectID (GUID) if available, otherwise URL
		var moduleIdXml string
		if obj.ModuleID.ObjectID != "" {
			isExtension := obj.ModuleID.ExtensionName != ""
			moduleType := "ConfigModule"
			if isExtension {
				moduleType = "ExtensionModule"
			}
			propertyIDXml := ""
			if obj.ModuleID.PropertyID != "" {
				propertyIDXml = fmt.Sprintf(`<propertyID xmlns="%s">%s</propertyID>`, nsBD, obj.ModuleID.PropertyID)
			}
			moduleIdXml = fmt.Sprintf(
				`<type xmlns="%s">%s</type>`+
					`<URL xmlns="%s"></URL>`+
					`<extensionName xmlns="%s">%s</extensionName>`+
					`<objectID xmlns="%s">%s</objectID>`+
					`%s`+
					`<extId xmlns="%s">0</extId>`,
				nsBD, moduleType,
				nsBD,
				nsBD, EscapeXML(obj.ModuleID.ExtensionName),
				nsBD, obj.ModuleID.ObjectID,
				propertyIDXml,
				nsBD,
			)
		} else {
			url := obj.ModuleID.URL
			if url == "" {
				url = fmt.Sprintf("e1cib/data/%s.%s", obj.ModuleID.Type, obj.ModuleID.Name)
			}
			moduleIdXml = fmt.Sprintf(
				`<type xmlns="%s">ConfigModule</type>`+
					`<URL xmlns="%s">%s</URL>`+
					`<extensionName xmlns="%s"></extensionName>`+
					`<extId xmlns="%s">0</extId>`,
				nsBD,
				nsBD, EscapeXML(url),
				nsBD,
				nsBD,
			)
		}

		modulesXml.WriteString(fmt.Sprintf(
			`<moduleBPInfo xmlns="%s"><id xmlns="%s">%s</id>%s</moduleBPInfo>`,
			nsBP, nsBP, moduleIdXml, bpLines.String(),
		))
	}

	return fmt.Sprintf(
		`%s<request xmlns="%s">`+
			`<infoBaseAlias>%s</infoBaseAlias>`+
			`<idOfDebuggerUI>%s</idOfDebuggerUI>`+
			`<bpWorkspace>%s</bpWorkspace>`+
			`</request>`,
		xmlHeader, ns,
		EscapeXML(alias),
		EscapeXML(id),
		modulesXml.String(),
	)
}

// BuildStepXML builds step request XML (continue, stepIn, stepOut, breakOnNextStatement).
func BuildStepXML(alias, id string, targetID TargetID, action string) string {
	return fmt.Sprintf(
		`%s<request xmlns="%s" xmlns:bd="%s" xmlns:xsi="%s">`+
			`<infoBaseAlias>%s</infoBaseAlias>`+
			`<idOfDebuggerUI>%s</idOfDebuggerUI>`+
			`<targetID xsi:type="bd:DebugTargetIdLight"><bd:id>%s</bd:id></targetID>`+
			`<action>%s</action>`+
			`<simple>false</simple>`+
			`</request>`,
		xmlHeader, ns, nsBD, nsXSI,
		EscapeXML(alias),
		EscapeXML(id),
		EscapeXML(targetID.ID),
		EscapeXML(action),
	)
}

// BuildAttachDetachTargetsXML builds attachDetachDbgTargets request XML.
// Sends multiple <id> elements, one per target.
func BuildAttachDetachTargetsXML(alias, id string, targetIDs []TargetID, attach bool) string {
	attachVal := "false"
	if attach {
		attachVal = "true"
	}
	var idsXml strings.Builder
	for _, tid := range targetIDs {
		idsXml.WriteString(fmt.Sprintf(
			`<id xsi:type="bd:DebugTargetIdLight"><bd:id>%s</bd:id></id>`,
			EscapeXML(tid.ID),
		))
	}
	return fmt.Sprintf(
		`%s<request xmlns="%s" xmlns:bd="%s" xmlns:xsi="%s">`+
			`<infoBaseAlias>%s</infoBaseAlias>`+
			`<idOfDebuggerUI>%s</idOfDebuggerUI>`+
			`<attach>%s</attach>`+
			`%s`+
			`</request>`,
		xmlHeader, ns, nsBD, nsXSI,
		EscapeXML(alias),
		EscapeXML(id),
		attachVal,
		idsXml.String(),
	)
}

// BuildGetCallStackXML builds getCallStack request XML.
// TypeScript uses: <id><id xmlns="NS_BD">uuid</id></id>
func BuildGetCallStackXML(alias, id string, targetID TargetID) string {
	return fmt.Sprintf(
		`%s<request xmlns="%s">`+
			`<infoBaseAlias>%s</infoBaseAlias>`+
			`<idOfDebuggerUI>%s</idOfDebuggerUI>`+
			`<id><id xmlns="%s">%s</id></id>`+
			`</request>`,
		xmlHeader, ns,
		EscapeXML(alias),
		EscapeXML(id),
		nsBD, EscapeXML(targetID.ID),
	)
}

// BuildClearBreakOnNextStatementXML builds clearBreakOnNextStatement request XML.
func BuildClearBreakOnNextStatementXML(alias, id string) string {
	return BuildRequestXML(alias, id, "")
}

// BuildEvalLocalVariablesXML builds evalLocalVariables request XML.
func BuildEvalLocalVariablesXML(alias, id string, targetID TargetID, resultID string) string {
	exprXml := fmt.Sprintf(
		`<expr xsi:type="calc:CalculationSourceDataStorage">`+
			`<calc:stackLevel>0</calc:stackLevel>`+
			`<calc:srcCalcInfo>`+
			`<calc:expressionResultID>%s</calc:expressionResultID>`+
			`<calc:interfaces>context</calc:interfaces>`+
			`</calc:srcCalcInfo>`+
			`<calc:presOptions>`+
			`<calc:maxTextSize>307200</calc:maxTextSize>`+
			`<calc:stopOnFirstEOL>false</calc:stopOnFirstEOL>`+
			`</calc:presOptions>`+
			`</expr>`,
		EscapeXML(resultID),
	)
	return fmt.Sprintf(
		`%s<request xmlns="%s" xmlns:bd="%s" xmlns:calc="%s" xmlns:xsi="%s">`+
			`<infoBaseAlias>%s</infoBaseAlias>`+
			`<idOfDebuggerUI>%s</idOfDebuggerUI>`+
			`<calcWaitingTime>5000</calcWaitingTime>`+
			`<targetID xsi:type="bd:DebugTargetIdLight"><bd:id>%s</bd:id></targetID>`+
			`%s`+
			`</request>`,
		xmlHeader, ns, nsBD, nsCalc, nsXSI,
		EscapeXML(alias),
		EscapeXML(id),
		EscapeXML(targetID.ID),
		exprXml,
	)
}

// BuildEvalExprXML builds evalExpr request XML.
func BuildEvalExprXML(alias, id string, targetID TargetID, expression, resultID string) string {
	exprXml := fmt.Sprintf(
		`<expr xsi:type="calc:CalculationSourceDataStorage">`+
			`<calc:stackLevel>0</calc:stackLevel>`+
			`<calc:srcCalcInfo>`+
			`<calc:expressionResultID>%s</calc:expressionResultID>`+
			`<calc:calcItem>`+
			`<calc:itemType>expression</calc:itemType>`+
			`<calc:expression>%s</calc:expression>`+
			`<calc:property></calc:property>`+
			`</calc:calcItem>`+
			`<calc:interfaces>context</calc:interfaces>`+
			`</calc:srcCalcInfo>`+
			`<calc:presOptions>`+
			`<calc:maxTextSize>307200</calc:maxTextSize>`+
			`<calc:stopOnFirstEOL>false</calc:stopOnFirstEOL>`+
			`</calc:presOptions>`+
			`</expr>`,
		EscapeXML(resultID),
		EscapeXML(expression),
	)
	return fmt.Sprintf(
		`%s<request xmlns="%s" xmlns:bd="%s" xmlns:calc="%s" xmlns:xsi="%s">`+
			`<infoBaseAlias>%s</infoBaseAlias>`+
			`<idOfDebuggerUI>%s</idOfDebuggerUI>`+
			`<calcWaitingTime>5000</calcWaitingTime>`+
			`<targetID xsi:type="bd:DebugTargetIdLight"><bd:id>%s</bd:id></targetID>`+
			`%s`+
			`</request>`,
		xmlHeader, ns, nsBD, nsCalc, nsXSI,
		EscapeXML(alias),
		EscapeXML(id),
		EscapeXML(targetID.ID),
		exprXml,
	)
}
