package markdown

import (
	"fmt"
	"testing"
)

func TestScratchListSpacing(t *testing.T) {
	InitRendering(DefaultRendering())
	for _, width := range []int{60, 25, 12} {
		for _, source := range []string{
			"- First item wraps onto a second row\n- b\n- c",
			"- first\n- second\n- third",
		} {
			got, err := Render(source, width)
			if err != nil {
				t.Fatal(err)
			}
			fmt.Printf("WIDTH %d SOURCE %q\n%q\n---\n", width, source, got)
		}
	}
}
