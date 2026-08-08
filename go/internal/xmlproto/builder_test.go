package xmlproto

import "testing"

func testWorkspace() *BPWorkspace {
	return &BPWorkspace{
		Objects: []BPObject{
			{
				ModuleID: ModuleID{
					Type:       "ОбщийМодуль",
					Name:       "ОбщийМодуль",
					ObjectID:   "11111111-2222-3333-4444-555555555555",
					PropertyID: ModulePropertyID["CommonModule"],
				},
				Lines: []int{42},
			},
		},
	}
}

func TestBuildSetBreakpointsXMLExtended(t *testing.T) {
	xml := BuildSetBreakpointsXML("infobase", "id", testWorkspace(), false)

	for _, want := range []string{"<line>42</line>", "<isActive>true</isActive>", "<breakOnCondition>false</breakOnCondition>", "<hitCount>1</hitCount>"} {
		if !contains(xml, want) {
			t.Errorf("extended bpInfo must contain %q, got:\n%s", want, xml)
		}
	}
}

func TestBuildSetBreakpointsXMLCompact(t *testing.T) {
	xml := BuildSetBreakpointsXML("infobase", "id", testWorkspace(), true)

	for _, want := range []string{"<line>42</line>", "<isActive>true</isActive>"} {
		if !contains(xml, want) {
			t.Errorf("compact bpInfo must contain %q, got:\n%s", want, xml)
		}
	}
	// Properties absent from the debugBreakpoints XDTO package of platforms before
	// 8.3.24 — their presence makes the platform reject the whole request.
	for _, unwanted := range []string{"breakOnCondition", "condition", "breakOnParentMethod", "parentMethod", "breakOnHitCount", "hitCountVariant", "hitCount", "temp"} {
		if contains(xml, "<"+unwanted+">") {
			t.Errorf("compact bpInfo must not contain <%s>, got:\n%s", unwanted, xml)
		}
	}
}

func TestExceptionDescr(t *testing.T) {
	body := `<?xml version="1.0" encoding="UTF-8"?><exception xmlns="http://v8.1c.ru/8.2/virtual-resource-system"` +
		` xmlns:d1p1="http://v8.1c.ru/8.1/data/core"><d1p1:descr>Ошибка преобразования данных XDTO:` + "\n" +
		`НачалоСвойства: {http://v8.1c.ru/8.3/debugger/debugBreakpoints}breakOnCondition</d1p1:descr></exception>`

	got := ExceptionDescr(body)
	want := "Ошибка преобразования данных XDTO: НачалоСвойства: {http://v8.1c.ru/8.3/debugger/debugBreakpoints}breakOnCondition"
	if got != want {
		t.Errorf("ExceptionDescr() = %q, want %q", got, want)
	}
}

func TestExceptionDescrPlainBody(t *testing.T) {
	if got := ExceptionDescr("  not xml  "); got != "not xml" {
		t.Errorf("ExceptionDescr() = %q, want %q", got, "not xml")
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
