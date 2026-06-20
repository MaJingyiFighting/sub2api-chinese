package apicompat

import (
	"strings"
	"testing"
)

// pushAll feeds every chunk through the extractor and returns the concatenated
// visible and reasoning text, including a final Flush.
func pushAll(e *ThinkTagExtractor, chunks ...string) (visible, reasoning string) {
	var vis, reason strings.Builder
	for _, ch := range chunks {
		v, r := e.Push(ch)
		vis.WriteString(v)
		reason.WriteString(r)
	}
	v, r := e.Flush()
	vis.WriteString(v)
	reason.WriteString(r)
	return vis.String(), reason.String()
}

func TestThinkTagExtractor_SingleBlock(t *testing.T) {
	e := &ThinkTagExtractor{}
	visible, reasoning := pushAll(e, "<think>secret</think>你好")
	if visible != "你好" {
		t.Fatalf("visible = %q, want %q", visible, "你好")
	}
	if reasoning != "secret" {
		t.Fatalf("reasoning = %q, want %q", reasoning, "secret")
	}
}

func TestThinkTagExtractor_CrossChunkOpenTag(t *testing.T) {
	e := &ThinkTagExtractor{}
	// The opening tag is split across chunk boundaries: "<thi" + "nk>...".
	visible, reasoning := pushAll(e, "<thi", "nk>secret</think>", "你好")
	if visible != "你好" {
		t.Fatalf("visible = %q, want %q", visible, "你好")
	}
	if strings.Contains(visible, "secret") {
		t.Fatalf("visible leaked reasoning: %q", visible)
	}
	if reasoning != "secret" {
		t.Fatalf("reasoning = %q, want %q", reasoning, "secret")
	}
}

func TestThinkTagExtractor_CrossChunkCloseTag(t *testing.T) {
	e := &ThinkTagExtractor{}
	visible, reasoning := pushAll(e, "<think>sec", "ret</thi", "nk>visible")
	if visible != "visible" {
		t.Fatalf("visible = %q, want %q", visible, "visible")
	}
	if reasoning != "secret" {
		t.Fatalf("reasoning = %q, want %q", reasoning, "secret")
	}
}

func TestThinkTagExtractor_MultipleBlocks(t *testing.T) {
	e := &ThinkTagExtractor{}
	visible, reasoning := pushAll(e, "<think>a</think>one<think>b</think>two")
	if visible != "onetwo" {
		t.Fatalf("visible = %q, want %q", visible, "onetwo")
	}
	if reasoning != "ab" {
		t.Fatalf("reasoning = %q, want %q", reasoning, "ab")
	}
	if strings.Contains(visible, "a") || strings.Contains(visible, "b") {
		t.Fatalf("visible leaked reasoning: %q", visible)
	}
}

func TestThinkTagExtractor_LeadingWhitespace(t *testing.T) {
	e := &ThinkTagExtractor{}
	visible, reasoning := pushAll(e, "  \n<think>secret</think>answer")
	if strings.Contains(visible, "secret") {
		t.Fatalf("visible leaked reasoning: %q", visible)
	}
	if !strings.Contains(visible, "answer") {
		t.Fatalf("visible missing answer: %q", visible)
	}
	if reasoning != "secret" {
		t.Fatalf("reasoning = %q, want %q", reasoning, "secret")
	}
}

func TestThinkTagExtractor_CaseInsensitive(t *testing.T) {
	e := &ThinkTagExtractor{}
	visible, reasoning := pushAll(e, "<THINK>secret</Think>你好")
	if visible != "你好" {
		t.Fatalf("visible = %q, want %q", visible, "你好")
	}
	if reasoning != "secret" {
		t.Fatalf("reasoning = %q, want %q", reasoning, "secret")
	}
}

func TestThinkTagExtractor_UnclosedThink(t *testing.T) {
	e := &ThinkTagExtractor{}
	// No closing tag: the body must stay in reasoning, never visible.
	visible, reasoning := pushAll(e, "<think>secret never closed")
	if visible != "" {
		t.Fatalf("visible = %q, want empty", visible)
	}
	if strings.Contains(visible, "secret") {
		t.Fatalf("visible leaked unclosed reasoning: %q", visible)
	}
	if reasoning != "secret never closed" {
		t.Fatalf("reasoning = %q, want %q", reasoning, "secret never closed")
	}
}

func TestThinkTagExtractor_DoesNotStripThinkingTag(t *testing.T) {
	e := &ThinkTagExtractor{}
	// `<thinking>` is NOT `<think>` and must be preserved verbatim.
	in := "before <thinking>kept</thinking> after"
	visible, reasoning := pushAll(e, in)
	if visible != in {
		t.Fatalf("visible = %q, want %q", visible, in)
	}
	if reasoning != "" {
		t.Fatalf("reasoning = %q, want empty", reasoning)
	}
}

func TestThinkTagExtractor_LookalikeTagsPassThrough(t *testing.T) {
	cases := []string{
		"a < b and c < d",          // bare '<' as math/text
		"<thinker>x</thinker>",     // diverges after "<think"
		"code: if (x<y) return; ok", // '<' inside code
		"<div>hello</div>",         // unrelated tag
	}
	for _, in := range cases {
		e := &ThinkTagExtractor{}
		visible, reasoning := pushAll(e, in)
		if visible != in {
			t.Fatalf("visible = %q, want %q", visible, in)
		}
		if reasoning != "" {
			t.Fatalf("reasoning = %q, want empty for input %q", reasoning, in)
		}
	}
}

func TestThinkTagExtractor_ByteAtATime(t *testing.T) {
	// Feed one byte at a time to exercise the cross-chunk buffering on every
	// tag boundary.
	e := &ThinkTagExtractor{}
	full := "x<think>hidden</think>y"
	var chunks []string
	for i := 0; i < len(full); i++ {
		chunks = append(chunks, full[i:i+1])
	}
	visible, reasoning := pushAll(e, chunks...)
	if visible != "xy" {
		t.Fatalf("visible = %q, want %q", visible, "xy")
	}
	if reasoning != "hidden" {
		t.Fatalf("reasoning = %q, want %q", reasoning, "hidden")
	}
}

func TestThinkTagExtractor_TrailingPartialOpenTagIsVisible(t *testing.T) {
	// A stream that ends mid-prefix with no closing was literal text.
	e := &ThinkTagExtractor{}
	visible, reasoning := pushAll(e, "hello <thi")
	if visible != "hello <thi" {
		t.Fatalf("visible = %q, want %q", visible, "hello <thi")
	}
	if reasoning != "" {
		t.Fatalf("reasoning = %q, want empty", reasoning)
	}
}

func TestThinkTagExtractor_NoTags(t *testing.T) {
	e := &ThinkTagExtractor{}
	visible, reasoning := pushAll(e, "plain ", "text ", "only")
	if visible != "plain text only" {
		t.Fatalf("visible = %q, want %q", visible, "plain text only")
	}
	if reasoning != "" {
		t.Fatalf("reasoning = %q, want empty", reasoning)
	}
}
