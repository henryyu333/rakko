//go:build darwin

package main

// defaultPinnedApps 是首次使用前从未编辑过固定列表时的候选：按顺序尝试，
// 只加入系统能解析出应用的那几个；某个默认应用缺失不会让整组失败。
var defaultPinnedApps = []string{
	"com.apple.finder",
	"com.apple.Safari",
	"com.apple.Terminal",
	"com.apple.Notes",
}
