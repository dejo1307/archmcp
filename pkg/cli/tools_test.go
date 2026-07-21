package cli

import (
	"strings"
	"testing"
)

func TestRenderToolList_EngineOnly(t *testing.T) {
	out := RenderToolList(ToolListSpec{})

	for _, tool := range OSSTools() {
		if !strings.Contains(out, tool.Name) {
			t.Errorf("tool %q missing from the default catalogue", tool.Name)
		}
	}
	// With no second group there is nothing to distinguish, so the engine's
	// tools are not labelled and no wrapper is referenced.
	for _, unwanted := range []string{"OSS tools:", "Enterprise", "ENOLA_LICENSE_KEY"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("default catalogue should not mention %q:\n%s", unwanted, out)
		}
	}
}

func TestRenderToolList_WithExtraGroup(t *testing.T) {
	out := RenderToolList(ToolListSpec{
		Extra:        []ToolEntry{{Name: "extra_tool", Description: "Does something extra."}},
		ExtraHeading: "Extra tools:",
	})

	if !strings.Contains(out, "OSS tools:") {
		t.Error("engine tools should be labelled once a second group exists")
	}
	if !strings.Contains(out, "Extra tools:") || !strings.Contains(out, "extra_tool") {
		t.Errorf("extra group missing:\n%s", out)
	}
	if !strings.Contains(out, "generate_snapshot") {
		t.Error("engine tools must still be listed alongside the extra group")
	}
}

// A wrapper tool with a long name widens the column for every group, so no row
// runs its description into the name.
func TestRenderToolList_LongNameWidensEveryGroup(t *testing.T) {
	const long = "a_very_long_wrapper_tool_name"
	out := RenderToolList(ToolListSpec{
		Extra:        []ToolEntry{{Name: long, Description: "Long."}},
		ExtraHeading: "Extra tools:",
	})

	for _, line := range strings.Split(out, "\n") {
		for _, name := range []string{"explore", long} {
			if !strings.HasPrefix(line, "  "+name+" ") {
				continue
			}
			desc := strings.Index(line, strings.TrimSpace(line[2+len(name):]))
			if want := 2 + len(long) + 2; desc != want {
				t.Errorf("%q description starts at column %d, want %d", name, desc, want)
			}
		}
	}
}

func TestRenderToolList_LockedGroup(t *testing.T) {
	out := RenderToolList(ToolListSpec{
		Extra:        []ToolEntry{{Name: "extra_tool", Description: "Does something extra."}},
		ExtraHeading: "Extra tools:",
		ExtraLocked:  true,
		LockedNote:   "Extra tools are not available.",
	})

	if strings.Contains(out, "extra_tool") {
		t.Error("locked tools must not be listed")
	}
	if !strings.Contains(out, "Extra tools are not available.") {
		t.Errorf("locked note missing:\n%s", out)
	}
	if !strings.Contains(out, "generate_snapshot") {
		t.Error("engine tools must still be listed when the extra group is locked")
	}
}
