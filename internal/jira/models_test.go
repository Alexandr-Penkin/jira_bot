package jira

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestADFDocument_ExtractText_Nil(t *testing.T) {
	var doc *ADFDocument
	assert.Equal(t, "", doc.ExtractText())
}

func TestADFDocument_ExtractText_Empty(t *testing.T) {
	doc := &ADFDocument{
		Type:    "doc",
		Version: 1,
		Content: nil,
	}
	assert.Equal(t, "", doc.ExtractText())
}

func TestADFDocument_ExtractText_SingleParagraph(t *testing.T) {
	doc := &ADFDocument{
		Type:    "doc",
		Version: 1,
		Content: []ADFNode{
			{
				Type: "paragraph",
				Content: []ADFNode{
					{Type: "text", Text: "Hello, world!"},
				},
			},
		},
	}
	assert.Equal(t, "Hello, world!\n", doc.ExtractText())
}

func TestADFDocument_ExtractText_MultipleParagraphs(t *testing.T) {
	doc := &ADFDocument{
		Type:    "doc",
		Version: 1,
		Content: []ADFNode{
			{
				Type: "paragraph",
				Content: []ADFNode{
					{Type: "text", Text: "First paragraph."},
				},
			},
			{
				Type: "paragraph",
				Content: []ADFNode{
					{Type: "text", Text: "Second paragraph."},
				},
			},
		},
	}
	assert.Equal(t, "First paragraph.\nSecond paragraph.\n", doc.ExtractText())
}

func TestADFDocument_ExtractText_Heading(t *testing.T) {
	doc := &ADFDocument{
		Type:    "doc",
		Version: 1,
		Content: []ADFNode{
			{
				Type: "heading",
				Content: []ADFNode{
					{Type: "text", Text: "My Heading"},
				},
			},
			{
				Type: "paragraph",
				Content: []ADFNode{
					{Type: "text", Text: "Body text."},
				},
			},
		},
	}
	assert.Equal(t, "My Heading\nBody text.\n", doc.ExtractText())
}

func TestADFDocument_ExtractText_NestedContent(t *testing.T) {
	doc := &ADFDocument{
		Type:    "doc",
		Version: 1,
		Content: []ADFNode{
			{
				Type: "paragraph",
				Content: []ADFNode{
					{Type: "text", Text: "Hello "},
					{Type: "text", Text: "world"},
				},
			},
		},
	}
	assert.Equal(t, "Hello world\n", doc.ExtractText())
}

func TestADFDocument_ExtractText_UnknownNodeType(t *testing.T) {
	doc := &ADFDocument{
		Type:    "doc",
		Version: 1,
		Content: []ADFNode{
			{
				Type: "bulletList",
				Content: []ADFNode{
					{
						Type: "listItem",
						Content: []ADFNode{
							{Type: "text", Text: "item 1"},
						},
					},
				},
			},
		},
	}
	// Unknown types do not add trailing newline
	assert.Equal(t, "item 1", doc.ExtractText())
}

func TestExtractNodeText_EmptyNode(t *testing.T) {
	node := ADFNode{Type: "paragraph"}
	assert.Equal(t, "\n", extractNodeText(node))
}

func TestExtractNodeText_TextNode(t *testing.T) {
	node := ADFNode{Type: "text", Text: "hello"}
	assert.Equal(t, "hello", extractNodeText(node))
}
