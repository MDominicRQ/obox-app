package escpos

import (
	"encoding/xml"
	"fmt"
	"io"
	"strings"
)

const maxEPOSItems = 4096

type xmlRawItem struct {
	XMLName xml.Name
	Attrs   []xml.Attr
	Content string
}

func readFlatElement(decoder *xml.Decoder, start xml.StartElement) (xmlRawItem, error) {
	item := xmlRawItem{
		XMLName: start.Name,
		Attrs:   append([]xml.Attr(nil), start.Attr...),
	}

	var text strings.Builder
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			return xmlRawItem{}, fmt.Errorf("XML parse error: unexpected end of <%s>", start.Name.Local)
		}
		if err != nil {
			return xmlRawItem{}, fmt.Errorf("XML parse error: %w", err)
		}

		switch t := token.(type) {
		case xml.CharData:
			text.Write([]byte(t))
		case xml.StartElement:
			return xmlRawItem{}, fmt.Errorf("nested element <%s> is not supported inside <%s>", t.Name.Local, start.Name.Local)
		case xml.EndElement:
			if t.Name != start.Name {
				return xmlRawItem{}, fmt.Errorf("unexpected closing element </%s>", t.Name.Local)
			}
			item.Content = text.String()
			return item, nil
		}
	}
}

func parseEPOSPrintFragment(fragment string) ([]xmlRawItem, error) {
	decoder := xml.NewDecoder(strings.NewReader(fragment))
	decoder.Strict = true

	var rootSeen bool
	items := make([]xmlRawItem, 0, 16)

	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("XML parse error: %w", err)
		}

		switch t := token.(type) {
		case xml.StartElement:
			if !rootSeen {
				if strings.ToLower(t.Name.Local) != "epos-print" {
					return nil, fmt.Errorf("expected <epos-print> root element")
				}
				rootSeen = true
				continue
			}

			if len(items) >= maxEPOSItems {
				return nil, fmt.Errorf("too many elements inside <epos-print>")
			}

			item, itemErr := readFlatElement(decoder, t)
			if itemErr != nil {
				return nil, itemErr
			}
			items = append(items, item)

		case xml.EndElement:
			if rootSeen && strings.ToLower(t.Name.Local) == "epos-print" {
				return items, nil
			}
		}
	}

	if !rootSeen {
		return nil, fmt.Errorf("no <epos-print> element found in request body")
	}
	return nil, fmt.Errorf("XML parse error: missing </epos-print>")
}

func ParseXML(body []byte) ([]byte, error) {
	s := string(body)
	start := strings.Index(s, "<epos-print")
	end := strings.LastIndex(s, "</epos-print>")
	if start == -1 || end == -1 || end < start {
		return nil, fmt.Errorf("no <epos-print> element found in request body")
	}
	fragment := s[start : end+len("</epos-print>")]

	items, err := parseEPOSPrintFragment(fragment)
	if err != nil {
		return nil, err
	}

	job := append([]byte(nil), CmdInit...)

	for _, item := range items {
		tag := strings.ToLower(item.XMLName.Local)
		attrs := attrMap(item.Attrs)

		switch tag {
		case "text":
			job = append(job, BuildText(item.Content, textAttrsFromMap(attrs))...)

		case "feed":
			lines := clamp(parseInt(attrs["line"], 1), 1, 255)
			for range lines {
				job = append(job, LF)
			}

		case "cut":
			job = append(job, CmdCut...)

		case "pulse":
			pulseCmd, err := BuildPulse(PulseAttrs{
				Drawer: attrs["drawer"],
				Time:   attrs["time"],
			})
			if err != nil {
				return nil, fmt.Errorf("pulse element: %w", err)
			}
			job = append(job, pulseCmd...)

		case "image":
			imgAttrs := ImageAttrs{
				Align:  attrs["align"],
				Width:  parseInt(attrs["width"], 0),
				Height: parseInt(attrs["height"], 0),
			}
			imgCmd, err := BuildImage(strings.TrimSpace(item.Content), imgAttrs)
			if err != nil {
				return nil, fmt.Errorf("image element: %w", err)
			}
			job = append(job, imgCmd...)

		default:
			return nil, fmt.Errorf("unsupported element <%s> inside <epos-print>", tag)
		}
	}

	return job, nil
}

func attrMap(attrs []xml.Attr) map[string]string {
	m := make(map[string]string, len(attrs))
	for _, a := range attrs {
		m[strings.ToLower(a.Name.Local)] = a.Value
	}
	return m
}

func parseInt(s string, def int) int {
	var n int
	if _, err := fmt.Sscanf(s, "%d", &n); err != nil {
		return def
	}
	return n
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func boolPtr(m map[string]string, key string) *bool {
	switch m[key] {
	case "1", "true":
		t := true
		return &t
	case "0", "false":
		f := false
		return &f
	}
	return nil
}

func textAttrsFromMap(m map[string]string) TextAttrs {
	return TextAttrs{
		Align:        m["align"],
		Font:         m["font"],
		Em:           boolPtr(m, "em"),
		Underline:    boolPtr(m, "ul"),
		DoubleWidth:  boolPtr(m, "dw"),
		DoubleHeight: boolPtr(m, "dh"),
	}
}
