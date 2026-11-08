//go:build darwin

package main

// The marks before each tool in 设置 → AI → Agent 完成提醒. Claude Code
// and Codex reuse the quota cards' marks; the rest are the tools' own marks:
// opencode and pi from their sites' favicons, omp from omp.sh's favicon
// (its gradient flattened to the middle stop), Gemini's spark and Grok's
// mark from their published brand SVGs, Devin's from devin.ai's favicon. Each viewBox is cropped to the
// artwork, so a box of settingsRowMark shows it at the row's mark size.

import (
	"github.com/egoist/mygo/ui"

	"rakko/internal/agentnotify"
	"rakko/internal/aiusage"
)

var (
	opencodeLogo = ui.MustParseSVG([]byte(`<svg viewBox="0 0 320 320" xmlns="http://www.w3.org/2000/svg"><path fill="white" fill-rule="evenodd" d="M288 320H32V0H288V320ZM224 64H96V256H224V64Z"/><path fill="white" fill-opacity="0.35" d="M224 128V256H96V128H224Z"/></svg>`))
	ompLogo      = ui.MustParseSVG([]byte(`<svg viewBox="0 0 40 40" xmlns="http://www.w3.org/2000/svg"><path fill="white" d="M2 2h36v8H28v32h-8V10h-6v22h-8V10H2z"/></svg>`))
	piLogo       = ui.MustParseSVG([]byte(`<svg viewBox="0 0 560 560" xmlns="http://www.w3.org/2000/svg"><path fill="white" d="M420 280H280V140H0V0H420V280Z"/><path fill="white" d="M560 560H420V280H560V560Z"/><path fill="white" d="M140 560H0V140H140V280H280V420H140V560Z"/></svg>`))
	geminiLogo   = ui.MustParseSVG([]byte(`<svg viewBox="0 0 24 24" xmlns="http://www.w3.org/2000/svg"><path fill="white" d="M20.616 10.835a14.147 14.147 0 01-4.45-3.001 14.111 14.111 0 01-3.678-6.452.503.503 0 00-.975 0 14.134 14.134 0 01-3.679 6.452 14.155 14.155 0 01-4.45 3.001c-.65.28-1.318.505-2.002.678a.502.502 0 000 .975c.684.172 1.35.397 2.002.677a14.147 14.147 0 014.45 3.001 14.112 14.112 0 013.679 6.453.502.502 0 00.975 0c.172-.685.397-1.351.677-2.003a14.145 14.145 0 013.001-4.45 14.113 14.113 0 016.453-3.678.503.503 0 000-.975 13.245 13.245 0 01-2.003-.678z"/></svg>`))
	grokLogo     = ui.MustParseSVG([]byte(`<svg viewBox="0 0 24 24" xmlns="http://www.w3.org/2000/svg"><path fill="white" fill-rule="evenodd" d="M9.27 15.29l7.978-5.897c.391-.29.95-.177 1.137.272.98 2.369.542 5.215-1.41 7.169-1.951 1.954-4.667 2.382-7.149 1.406l-2.711 1.257c3.889 2.661 8.611 2.003 11.562-.953 2.341-2.344 3.066-5.539 2.388-8.42l.006.007c-.983-4.232.242-5.924 2.75-9.383.06-.082.12-.164.179-.248l-3.301 3.305v-.01L9.267 15.292M7.623 16.723c-2.792-2.67-2.31-6.801.071-9.184 1.761-1.763 4.647-2.483 7.166-1.425l2.705-1.25a7.808 7.808 0 00-1.829-1A8.975 8.975 0 005.984 5.83c-2.533 2.536-3.33 6.436-1.962 9.764 1.022 2.487-.653 4.246-2.34 6.022-.599.63-1.199 1.259-1.682 1.925l7.62-6.815"/></svg>`))

	devinLogo = ui.MustParseSVG([]byte(`<svg viewBox="0 0 425 425" xmlns="http://www.w3.org/2000/svg"><path fill="white" d="M70 159.333V91.3471C70 88.3592 71.594 85.5983 74.1816 84.1044L133.043 50.1205C135.631 48.6265 138.819 48.6265 141.407 50.1205L200.269 84.1044C202.856 85.5983 204.45 88.3592 204.45 91.3471V126.068C204.708 137.606 210.806 148.734 221.531 154.926C232.256 161.117 244.942 160.834 255.063 155.289L285.132 137.929C287.719 136.435 290.907 136.435 293.495 137.929L352.357 171.913C354.944 173.406 356.538 176.167 356.538 179.155V247.123C356.538 250.111 354.944 252.872 352.357 254.366L293.495 288.35C290.907 289.844 287.719 289.844 285.132 288.35L255.306 271.13C245.146 265.456 232.344 265.117 221.534 271.358C210.809 277.55 204.711 288.678 204.453 300.215V334.926C204.453 337.914 202.859 340.675 200.271 342.169L141.41 376.153C138.822 377.647 135.634 377.647 133.046 376.153L74.1845 342.169C71.5969 340.675 70.0028 337.914 70.0028 334.926V266.959C70.0029 263.971 71.5969 261.21 74.1845 259.716L133.046 225.732C135.634 224.238 138.822 224.238 141.41 225.732L171.547 243.132C181.656 248.638 194.306 248.906 205.005 242.729C215.815 236.488 221.922 225.231 222.088 213.595C221.83 202.057 215.732 189.737 205.008 183.545C194.283 177.353 181.597 177.636 171.476 183.181L141.269 200.72C138.67 202.229 135.461 202.228 132.864 200.716L74.1576 166.562C71.5835 165.065 70 162.311 70 159.333Z"/></svg>`))

	ompColor    = ui.Hex("#9B4DFF") // omp.sh favicon gradient's middle stop
	geminiColor = ui.Hex("#3186FF") // Gemini spark blue
	qwenColor   = ui.Hex("#615CED") // Qwen's brand violet
	kiroColor   = ui.Hex("#9046FF") // kiro.dev icon's tile
)

// agentLogo is a tool's mark, its colour, and the icon box that shows the
// mark at settingsRowMark (see toolLogo for why boxes differ).
func agentLogo(t agentnotify.Tool) (*ui.SVG, ui.Color, float32) {
	switch t {
	case agentnotify.ToolClaude:
		return toolLogo(aiusage.ToolClaude)
	case agentnotify.ToolCodex:
		return toolLogo(aiusage.ToolCodex)
	case agentnotify.ToolOpencode:
		return opencodeLogo, ink, settingsRowMark
	case agentnotify.ToolOmp:
		return ompLogo, ompColor, settingsRowMark
	case agentnotify.ToolPi:
		return piLogo, ink, settingsRowMark
	case agentnotify.ToolGemini:
		return geminiLogo, geminiColor, settingsRowMark
	case agentnotify.ToolGrok:
		return grokLogo, ink, settingsRowMark
	case agentnotify.ToolDevin:
		// The favicon's mark spans 329 of its 425 units.
		return devinLogo, ink, settingsRowMark * 425 / 329
	case agentnotify.ToolCursor:
		return cursorLogo, ink, settingsRowMark
	case agentnotify.ToolCopilot:
		return copilotLogo, ink, settingsRowMark
	case agentnotify.ToolAntigravity:
		return antigravityLogo, ink, settingsRowMark
	case agentnotify.ToolQwen:
		return qwenLogo, qwenColor, settingsRowMark
	case agentnotify.ToolKiro:
		return kiroLogo, kiroColor, settingsRowMark
	case agentnotify.ToolFactory:
		// The icon's mark spans about 338 of its 508 units.
		return factoryLogo, ink, settingsRowMark * 508 / 338
	case agentnotify.ToolAugment:
		return augmentLogo, ink, settingsRowMark
	}
	return nil, ink, settingsRowMark
}
