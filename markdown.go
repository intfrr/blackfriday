//
// Black Friday Markdown Processor
// Originally based on http://github.com/tanoku/upskirt
// by Russ Ross <russ@russross.com>
//

//
//
// Markdown parsing and processing
//
//

package blackfriday

import (
	"bytes"
	"unicode"
)

// These are the supported markdown parsing extensions.
// OR these values together to select multiple extensions.
const (
	EXTENSION_NO_INTRA_EMPHASIS = 1 << iota
	EXTENSION_TABLES
	EXTENSION_FENCED_CODE
	EXTENSION_AUTOLINK
	EXTENSION_STRIKETHROUGH
	EXTENSION_LAX_HTML_BLOCKS
	EXTENSION_SPACE_HEADERS
)

// These are the possible flag values for the link renderer.
// Only a single one of these values will be used; they are not ORed together.
// These are mostly of interest if you are writing a new output format.
const (
	LINK_TYPE_NOT_AUTOLINK = iota
	LINK_TYPE_NORMAL
	LINK_TYPE_EMAIL
)

// These are the possible flag values for the listitem renderer.
// Multiple flag values may be ORed together.
// These are mostly of interest if you are writing a new output format.
const (
	LIST_TYPE_ORDERED = 1 << iota
	LIST_ITEM_CONTAINS_BLOCK
	LIST_ITEM_END_OF_LIST
)

// These are the possible flag values for the table cell renderer.
// Only a single one of these values will be used; they are not ORed together.
// These are mostly of interest if you are writing a new output format.
const (
	TABLE_ALIGNMENT_LEFT = 1 << iota
	TABLE_ALIGNMENT_RIGHT
	TABLE_ALIGNMENT_CENTER = (TABLE_ALIGNMENT_LEFT | TABLE_ALIGNMENT_RIGHT)
)

// The size of a tab stop.
const TAB_SIZE = 4

// These are the tags that are recognized as HTML block tags.
// Any of these can be included in markdown text without special escaping.
var block_tags = map[string]bool{
	"p":          true,
	"dl":         true,
	"h1":         true,
	"h2":         true,
	"h3":         true,
	"h4":         true,
	"h5":         true,
	"h6":         true,
	"ol":         true,
	"ul":         true,
	"del":        true,
	"div":        true,
	"ins":        true,
	"pre":        true,
	"form":       true,
	"math":       true,
	"table":      true,
	"iframe":     true,
	"script":     true,
	"fieldset":   true,
	"noscript":   true,
	"blockquote": true,
}

// This struct defines the rendering interface.
// A series of callback functions are registered to form a complete renderer.
// A single interface{} value field is provided, and that value is handed to
// each callback. Leaving a field blank suppresses rendering that type of output
// except where noted.
//
// This is mostly of interest if you are implementing a new rendering format.
// Most users will use the convenience functions to fill in this structure.
type Renderer struct {
	// block-level callbacks---nil skips the block
	blockcode  func(out *bytes.Buffer, text []byte, lang string, opaque interface{})
	blockquote func(out *bytes.Buffer, text []byte, opaque interface{})
	blockhtml  func(out *bytes.Buffer, text []byte, opaque interface{})
	header     func(out *bytes.Buffer, text []byte, level int, opaque interface{})
	hrule      func(out *bytes.Buffer, opaque interface{})
	list       func(out *bytes.Buffer, text []byte, flags int, opaque interface{})
	listitem   func(out *bytes.Buffer, text []byte, flags int, opaque interface{})
	paragraph  func(out *bytes.Buffer, text []byte, opaque interface{})
	table      func(out *bytes.Buffer, header []byte, body []byte, opaque interface{})
	tableRow   func(out *bytes.Buffer, text []byte, opaque interface{})
	tableCell  func(out *bytes.Buffer, text []byte, flags int, opaque interface{})

	// span-level callbacks---nil or return 0 prints the span verbatim
	autolink       func(out *bytes.Buffer, link []byte, kind int, opaque interface{}) int
	codespan       func(out *bytes.Buffer, text []byte, opaque interface{}) int
	doubleEmphasis func(out *bytes.Buffer, text []byte, opaque interface{}) int
	emphasis       func(out *bytes.Buffer, text []byte, opaque interface{}) int
	image          func(out *bytes.Buffer, link []byte, title []byte, alt []byte, opaque interface{}) int
	linebreak      func(out *bytes.Buffer, opaque interface{}) int
	link           func(out *bytes.Buffer, link []byte, title []byte, content []byte, opaque interface{}) int
	rawHtmlTag     func(out *bytes.Buffer, tag []byte, opaque interface{}) int
	tripleEmphasis func(out *bytes.Buffer, text []byte, opaque interface{}) int
	strikethrough  func(out *bytes.Buffer, text []byte, opaque interface{}) int

	// low-level callbacks---nil copies input directly into the output
	entity     func(out *bytes.Buffer, entity []byte, opaque interface{})
	normalText func(out *bytes.Buffer, text []byte, opaque interface{})

	// header and footer
	documentHeader func(out *bytes.Buffer, opaque interface{})
	documentFooter func(out *bytes.Buffer, opaque interface{})

	// user data---passed back to every callback
	opaque interface{}
}

type inlineParser func(out *bytes.Buffer, rndr *render, data []byte, offset int) int

type render struct {
	mk         *Renderer
	refs       map[string]*reference
	inline     [256]inlineParser
	flags      uint32
	nesting    int
	maxNesting int
}


//
//
// Public interface
//
//

// Parse and render a block of markdown-encoded text.
// The renderer is used to format the output, and extensions dictates which
// non-standard extensions are enabled.
func Markdown(input []byte, renderer *Renderer, extensions uint32) []byte {
	// no point in parsing if we can't render
	if renderer == nil {
		return nil
	}

	// fill in the render structure
	rndr := new(render)
	rndr.mk = renderer
	rndr.flags = extensions
	rndr.refs = make(map[string]*reference)
	rndr.maxNesting = 16

	// register inline parsers
	if rndr.mk.emphasis != nil || rndr.mk.doubleEmphasis != nil || rndr.mk.tripleEmphasis != nil {
		rndr.inline['*'] = inlineEmphasis
		rndr.inline['_'] = inlineEmphasis
		if extensions&EXTENSION_STRIKETHROUGH != 0 {
			rndr.inline['~'] = inlineEmphasis
		}
	}
	if rndr.mk.codespan != nil {
		rndr.inline['`'] = inlineCodespan
	}
	if rndr.mk.linebreak != nil {
		rndr.inline['\n'] = inlineLinebreak
	}
	if rndr.mk.image != nil || rndr.mk.link != nil {
		rndr.inline['['] = inlineLink
	}
	rndr.inline['<'] = inlineLangle
	rndr.inline['\\'] = inlineEscape
	rndr.inline['&'] = inlineEntity

	if extensions&EXTENSION_AUTOLINK != 0 {
		rndr.inline['h'] = inlineAutolink // http, https
		rndr.inline['H'] = inlineAutolink

		rndr.inline['f'] = inlineAutolink // ftp
		rndr.inline['F'] = inlineAutolink

		rndr.inline['m'] = inlineAutolink // mailto
		rndr.inline['M'] = inlineAutolink
	}

	// first pass: look for references, copy everything else
	text := bytes.NewBuffer(nil)
	beg, end := 0, 0
	for beg < len(input) { // iterate over lines
		if end = isReference(rndr, input[beg:]); end > 0 {
			beg += end
		} else { // skip to the next line
			end = beg
			for end < len(input) && input[end] != '\n' && input[end] != '\r' {
				end++
			}

			// add the line body if present
			if end > beg {
				expandTabs(text, input[beg:end])
			}

			for end < len(input) && (input[end] == '\n' || input[end] == '\r') {
				// add one \n per newline
				if input[end] == '\n' || (end+1 < len(input) && input[end+1] != '\n') {
					text.WriteByte('\n')
				}
				end++
			}

			beg = end
		}
	}

	// second pass: actual rendering
	output := bytes.NewBuffer(nil)
	if rndr.mk.documentHeader != nil {
		rndr.mk.documentHeader(output, rndr.mk.opaque)
	}

	if text.Len() > 0 {
		// add a final newline if not already present
		finalchar := text.Bytes()[text.Len()-1]
		if finalchar != '\n' && finalchar != '\r' {
			text.WriteByte('\n')
		}
		parseBlock(output, rndr, text.Bytes())
	}

	if rndr.mk.documentFooter != nil {
		rndr.mk.documentFooter(output, rndr.mk.opaque)
	}

	if rndr.nesting != 0 {
		panic("Nesting level did not end at zero")
	}

	return output.Bytes()
}


//
// Link references
//
// This section implements support for references that (usually) appear
// as footnotes in a document, and can be referenced anywhere in the document.
// The basic format is:
//
//    [1]: http://www.google.com/ "Google"
//    [2]: http://www.github.com/ "Github"
//
// Anywhere in the document, the reference can be linked by referring to its
// label, i.e., 1 and 2 in this example, as in:
//
//    This library is hosted on [Github][2], a git hosting site.

// References are parsed and stored in this struct.
type reference struct {
	link  []byte
	title []byte
}

// Compare two []byte values (case-insensitive), returning
// true if a is less than b.
func less(a []byte, b []byte) bool {
	// adapted from bytes.Compare in stdlib
	m := len(a)
	if m > len(b) {
		m = len(b)
	}
	for i, ac := range a[0:m] {
		// do a case-insensitive comparison
		ai, bi := unicode.ToLower(int(ac)), unicode.ToLower(int(b[i]))
		switch {
		case ai > bi:
			return false
		case ai < bi:
			return true
		}
	}
	switch {
	case len(a) < len(b):
		return true
	case len(a) > len(b):
		return false
	}
	return false
}

// Check whether or not data starts with a reference link.
// If so, it is parsed and stored in the list of references
// (in the render struct).
// Returns the number of bytes to skip to move past it, or zero
// if there is the first line is not a reference.
func isReference(rndr *render, data []byte) int {
	// up to 3 optional leading spaces
	if len(data) < 4 {
		return 0
	}
	i := 0
	for i < 3 && data[i] == ' ' {
		i++
	}
	if data[i] == ' ' {
		return 0
	}

	// id part: anything but a newline between brackets
	if data[i] != '[' {
		return 0
	}
	i++
	id_offset := i
	for i < len(data) && data[i] != '\n' && data[i] != '\r' && data[i] != ']' {
		i++
	}
	if i >= len(data) || data[i] != ']' {
		return 0
	}
	id_end := i

	// spacer: colon (space | tab)* newline? (space | tab)*
	i++
	if i >= len(data) || data[i] != ':' {
		return 0
	}
	i++
	for i < len(data) && (data[i] == ' ' || data[i] == '\t') {
		i++
	}
	if i < len(data) && (data[i] == '\n' || data[i] == '\r') {
		i++
		if i < len(data) && data[i] == '\n' && data[i-1] == '\r' {
			i++
		}
	}
	for i < len(data) && (data[i] == ' ' || data[i] == '\t') {
		i++
	}
	if i >= len(data) {
		return 0
	}

	// link: whitespace-free sequence, optionally between angle brackets
	if data[i] == '<' {
		i++
	}
	link_offset := i
	for i < len(data) && data[i] != ' ' && data[i] != '\t' && data[i] != '\n' && data[i] != '\r' {
		i++
	}
	link_end := i
	if data[link_offset] == '<' && data[link_end-1] == '>' {
		link_offset++
		link_end--
	}

	// optional spacer: (space | tab)* (newline | '\'' | '"' | '(' )
	for i < len(data) && (data[i] == ' ' || data[i] == '\t') {
		i++
	}
	if i < len(data) && data[i] != '\n' && data[i] != '\r' && data[i] != '\'' && data[i] != '"' && data[i] != '(' {
		return 0
	}

	// compute end-of-line
	line_end := 0
	if i >= len(data) || data[i] == '\r' || data[i] == '\n' {
		line_end = i
	}
	if i+1 < len(data) && data[i] == '\r' && data[i+1] == '\n' {
		line_end++
	}

	// optional (space|tab)* spacer after a newline
	if line_end > 0 {
		i = line_end + 1
		for i < len(data) && (data[i] == ' ' || data[i] == '\t') {
			i++
		}
	}

	// optional title: any non-newline sequence enclosed in '"() alone on its line
	title_offset, title_end := 0, 0
	if i+1 < len(data) && (data[i] == '\'' || data[i] == '"' || data[i] == '(') {
		i++
		title_offset = i

		// look for EOL
		for i < len(data) && data[i] != '\n' && data[i] != '\r' {
			i++
		}
		if i+1 < len(data) && data[i] == '\n' && data[i+1] == '\r' {
			title_end = i + 1
		} else {
			title_end = i
		}

		// step back
		i--
		for i > title_offset && (data[i] == ' ' || data[i] == '\t') {
			i--
		}
		if i > title_offset && (data[i] == '\'' || data[i] == '"' || data[i] == ')') {
			line_end = title_end
			title_end = i
		}
	}
	if line_end == 0 { // garbage after the link
		return 0
	}

	// a valid ref has been found
	if rndr == nil {
		return line_end
	}

	// id matches are case-insensitive
	id := string(bytes.ToLower(data[id_offset:id_end]))
	rndr.refs[id] = &reference{
		link:  data[link_offset:link_end],
		title: data[title_offset:title_end],
	}

	return line_end
}


//
//
// Miscellaneous helper functions
//
//


// Test if a character is a punctuation symbol.
// Taken from a private function in regexp in the stdlib.
func ispunct(c byte) bool {
	for _, r := range []byte("!\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~") {
		if c == r {
			return true
		}
	}
	return false
}

// Test if a character is a whitespace character.
func isspace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v'
}

// Test if a character is a letter or a digit.
// TODO: check when this is looking for ASCII alnum and when it should use unicode
func isalnum(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// Replace tab characters with spaces, aligning to the next TAB_SIZE column.
// TODO: count runes rather than bytes
func expandTabs(out *bytes.Buffer, line []byte) {
	i, tab := 0, 0

	for i < len(line) {
		org := i
		for i < len(line) && line[i] != '\t' {
			i++
			tab++
		}

		if i > org {
			out.Write(line[org:i])
		}

		if i >= len(line) {
			break
		}

		for {
			out.WriteByte(' ')
			tab++
			if tab%TAB_SIZE == 0 {
				break
			}
		}

		i++
	}
}
