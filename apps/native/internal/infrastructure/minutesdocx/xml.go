package minutesdocx

import (
	"bytes"
	"encoding/xml"
	"io"
	"strings"
)

// RawToken preserves Word's namespace prefixes and their declarations, so
// template-specific w14/w15 attributes and relationship IDs stay intact.
type node struct {
	name     string
	attrs    []xml.Attr
	children []*node
	text     string
}

func qualified(name xml.Name) string {
	if name.Space != "" {
		return name.Space + ":" + name.Local
	}
	return name.Local
}
func local(name string) string {
	_, after, ok := strings.Cut(name, ":")
	if ok {
		return after
	}
	return name
}
func parse(data []byte) (*node, error) {
	d := xml.NewDecoder(bytes.NewReader(data))
	var root *node
	stack := []*node{}
	for {
		token, err := d.RawToken()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, ErrDocument
		}
		switch t := token.(type) {
		case xml.StartElement:
			n := &node{name: qualified(t.Name), attrs: append([]xml.Attr{}, t.Attr...)}
			if len(stack) == 0 {
				if root != nil {
					return nil, ErrDocument
				}
				root = n
			} else {
				parent := stack[len(stack)-1]
				parent.children = append(parent.children, n)
			}
			stack = append(stack, n)
		case xml.EndElement:
			if len(stack) == 0 || stack[len(stack)-1].name != qualified(t.Name) {
				return nil, ErrDocument
			}
			stack = stack[:len(stack)-1]
		case xml.CharData:
			if len(stack) > 0 {
				parent := stack[len(stack)-1]
				if strings.TrimSpace(string(t)) != "" || local(parent.name) == "t" {
					parent.children = append(parent.children, &node{text: string(t)})
				}
			}
		case xml.Directive:
			return nil, ErrDocument
		}
	}
	if root == nil || len(stack) != 0 {
		return nil, ErrDocument
	}
	return root, nil
}
func (n *node) clone() *node {
	c := &node{name: n.name, text: n.text, attrs: append([]xml.Attr{}, n.attrs...)}
	for _, child := range n.children {
		c.children = append(c.children, child.clone())
	}
	return c
}
func (n *node) elements(name string) []*node {
	values := []*node{}
	for _, c := range n.children {
		if local(c.name) == name {
			values = append(values, c)
		}
	}
	return values
}
func (n *node) first(name string) *node {
	for _, c := range n.children {
		if local(c.name) == name {
			return c
		}
	}
	return nil
}
func (n *node) removeElements(name string) {
	kept := []*node{}
	for _, c := range n.children {
		if local(c.name) != name {
			kept = append(kept, c)
		}
	}
	n.children = kept
}
func (n *node) replaceText(value string) bool {
	for _, c := range n.children {
		if c.name == "" {
			c.text = value
			return true
		}
		if c.replaceText(value) {
			return true
		}
	}
	return false
}
func (n *node) setText(value string) {
	if !n.replaceText(value) {
		n.children = append(n.children, &node{name: "w:p", children: []*node{{name: "w:r", children: []*node{{name: "w:t", children: []*node{{text: value}}}}}}})
	}
}
func cloneText(n *node, value string) *node { c := n.clone(); c.setText(value); return c }
func (n *node) write(w *bytes.Buffer) {
	if n.name == "" {
		_ = xml.EscapeText(w, []byte(n.text))
		return
	}
	w.WriteByte('<')
	w.WriteString(n.name)
	for _, a := range n.attrs {
		w.WriteByte(' ')
		w.WriteString(qualified(a.Name))
		w.WriteString(`="`)
		_ = xml.EscapeText(w, []byte(a.Value))
		w.WriteByte('"')
	}
	w.WriteByte('>')
	for _, c := range n.children {
		c.write(w)
	}
	w.WriteString("</")
	w.WriteString(n.name)
	w.WriteByte('>')
}
