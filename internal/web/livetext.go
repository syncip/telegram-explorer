package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// The live editor exchanges edits as CodeMirror change sets in their JSON form:
//
//	[5, [2], [0, "neu"], 3]
//
// A number keeps that many characters, [n] deletes n, and [n, "a", "b"]
// replaces n characters with the lines "a" and "b" joined by a line break.
// Positions count UTF-16 code units, as in JavaScript, so the server keeps the
// text in that form.

// changePart is one section of a change set.
type changePart struct {
	keep int      // characters kept, when ins is nil and del is 0
	del  int      // characters removed
	ins  []uint16 // text inserted
	edit bool     // a replacement, not a kept section
}

type changeSet []changePart

var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

var errBadChange = errors.New("ungültige Änderung")

func parseChanges(raw json.RawMessage) (changeSet, error) {
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, errBadChange
	}
	cs := make(changeSet, 0, len(items))
	for _, it := range items {
		it = bytes.TrimSpace(it)
		if len(it) > 0 && it[0] == '[' {
			var arr []json.RawMessage
			if err := json.Unmarshal(it, &arr); err != nil || len(arr) == 0 {
				return nil, errBadChange
			}
			var del int
			if err := json.Unmarshal(arr[0], &del); err != nil || del < 0 {
				return nil, errBadChange
			}
			p := changePart{del: del, edit: true}
			if len(arr) > 1 {
				lines := make([]string, len(arr)-1)
				for i, l := range arr[1:] {
					// Line breaks only come between lines; one inside would count
					// differently in the editors.
					if err := json.Unmarshal(l, &lines[i]); err != nil || strings.ContainsAny(lines[i], "\r\n") {
						return nil, errBadChange
					}
				}
				p.ins = utf16.Encode([]rune(strings.Join(lines, "\n")))
			}
			cs = append(cs, p)
			continue
		}
		var keep int
		if err := json.Unmarshal(it, &keep); err != nil || keep < 0 {
			return nil, errBadChange
		}
		cs = append(cs, changePart{keep: keep})
	}
	return cs, nil
}

// lenBefore is the length of the text the change set applies to.
func (cs changeSet) lenBefore() int {
	n := 0
	for _, p := range cs {
		n += p.keep + p.del
	}
	return n
}

// apply returns the changed text, or an error if the change set was made for
// a text of another length.
func (cs changeSet) apply(doc []uint16) ([]uint16, error) {
	if cs.lenBefore() != len(doc) {
		return nil, errBadChange
	}
	out := make([]uint16, 0, len(doc))
	pos := 0
	for _, p := range cs {
		if !p.edit {
			out = append(out, doc[pos:pos+p.keep]...)
			pos += p.keep
			continue
		}
		out = append(out, p.ins...)
		pos += p.del
	}
	return out, nil
}

// mapPos moves a position in the old text to the new one. A position at an
// insertion stays in front of it, and one inside a deletion moves to its start,
// like CodeMirror's mapPos with assoc -1.
func (cs changeSet) mapPos(pos int) int {
	a, b := 0, 0
	for _, p := range cs {
		if !p.edit {
			if pos <= a+p.keep {
				return b + pos - a
			}
			a += p.keep
			b += p.keep
			continue
		}
		switch {
		case pos == a:
			return b
		case pos < a+p.del:
			return b
		case pos == a+p.del:
			return b + len(p.ins)
		}
		a += p.del
		b += len(p.ins)
	}
	return b + pos - a
}

// textOf decodes a file for the editor: UTF-8 only, with the byte order mark
// and the line break style kept aside so a save writes them back.
func textOf(data []byte) (text []uint16, bom, crlf bool, err error) {
	if bytes.HasPrefix(data, utf8BOM) {
		bom = true
		data = data[3:]
	}
	if !utf8.Valid(data) {
		return nil, false, false, badRequest("Die Datei ist kein Text in UTF-8 und lässt sich hier nicht bearbeiten.")
	}
	s := string(data)
	crlf = strings.Contains(s, "\r\n")
	// The editor knows only \n, a lone \r would count as a line break there.
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return utf16.Encode([]rune(s)), bom, crlf, nil
}

// fileOf is the reverse of textOf.
func fileOf(text []uint16, bom, crlf bool) []byte {
	s := string(utf16.Decode(text))
	if crlf {
		s = strings.ReplaceAll(s, "\n", "\r\n")
	}
	if bom {
		s = string(utf8BOM) + s
	}
	return []byte(s)
}
