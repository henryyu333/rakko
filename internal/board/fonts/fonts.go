package fonts

import (
	_ "embed"
	"sync"

	"github.com/egoist/mygo/ui"
)

//go:embed ShantellSans.ttf
var shantellSansTTF []byte

//go:embed YozaiSubset.ttf
var yozaiSubsetTTF []byte

var registerOnce sync.Once

// Register installs the embedded Latin and Chinese handwriting fonts for board text layout and drawing.
func Register() {
	registerOnce.Do(func() {
		_ = ui.RegisterFont(shantellSansTTF, "Shantell Sans")
		_ = ui.RegisterFont(yozaiSubsetTTF, "Yozai")
	})
}
