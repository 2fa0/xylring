package main

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
)

func makeTestPNG(width, height int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: 120, A: 255})
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

func TestIsImageFile(t *testing.T) {
	images := []string{
		"logo.png", "icon.PNG", "banner.jpg", "photo.JPEG", "photo.jpe",
		"anim.gif", "graphic.webp", "drawing.bmp", "vector.svg",
		"path/to/nested/app.png",
	}
	for _, p := range images {
		if !isImageFile(p) {
			t.Errorf("isImageFile(%q) should be true", p)
		}
	}

	nonImages := []string{
		"main.go", "README.md", "app.exe", "data.bin", "archive.zip", "script.sh", "",
	}
	for _, p := range nonImages {
		if isImageFile(p) {
			t.Errorf("isImageFile(%q) should be false", p)
		}
	}
}

func TestDecodeImageInfo(t *testing.T) {
	pngData := makeTestPNG(120, 80)
	info, err := decodeImageInfo(pngData, "test.png")
	if err != nil {
		t.Fatalf("decodeImageInfo failed: %v", err)
	}
	if info.width != 120 || info.height != 80 {
		t.Fatalf("unexpected dimensions: %d × %d", info.width, info.height)
	}
	if info.format != "PNG" {
		t.Fatalf("unexpected format: %s", info.format)
	}
	if info.size != int64(len(pngData)) {
		t.Fatalf("unexpected size: %d vs %d", info.size, len(pngData))
	}

	// SVG test
	svgData := []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="64" height="48" viewBox="0 0 64 48"><rect width="64" height="48" fill="red"/></svg>`)
	svgInfo, err := decodeImageInfo(svgData, "icon.svg")
	if err != nil {
		t.Fatalf("decodeImageInfo(svg) failed: %v", err)
	}
	if svgInfo.width != 64 || svgInfo.height != 48 {
		t.Fatalf("unexpected SVG dimensions: %d × %d", svgInfo.width, svgInfo.height)
	}

	// Corrupted data
	_, err = decodeImageInfo([]byte("not an image at all"), "bad.png")
	if err == nil {
		t.Fatal("expected error for corrupted image data")
	}

	// Empty data
	_, err = decodeImageInfo(nil, "empty.png")
	if err == nil {
		t.Fatal("expected error for empty image data")
	}
}

func TestFormatFileSizeAndDiff(t *testing.T) {
	if s := formatFileSize(500); s != "500 B" {
		t.Errorf("expected 500 B, got %s", s)
	}
	if s := formatFileSize(1536); s != "1.5 KB" {
		t.Errorf("expected 1.5 KB, got %s", s)
	}
	if s := formatFileSize(2 * 1024 * 1024); s != "2.00 MB" {
		t.Errorf("expected 2.00 MB, got %s", s)
	}

	diff, decreased := formatSizeDiff(1000, 2000)
	if decreased || diff != "+1000 B" {
		t.Errorf("unexpected size increase: diff=%s decreased=%v", diff, decreased)
	}

	diff, decreased = formatSizeDiff(3000, 1000)
	if !decreased || diff != "-1.9 KB" && diff != "-2.0 KB" {
		t.Errorf("unexpected size decrease: diff=%s decreased=%v", diff, decreased)
	}

	diff, _ = formatSizeDiff(1000, 1000)
	if diff != "大小相同" {
		t.Errorf("unexpected equal size: diff=%s", diff)
	}
}

func TestImageDiffUIRendering(t *testing.T) {
	tab := newTab(1, "/tmp/mock-repo")
	tab.loaded = true
	tab.branch = "main"
	tab.commits = []commit{
		{hash: hashN(1), author: "Dev", subject: "Update logo", when: 1700000000},
	}
	tab.graph.add(&tab.commits[0])
	tab.inspecting = hashN(1)

	oldPNG := makeTestPNG(50, 50)
	newPNG := makeTestPNG(100, 100)

	oldInfo, _ := decodeImageInfo(oldPNG, "logo.png")
	newInfo, _ := decodeImageInfo(newPNG, "logo.png")

	tab.compareFiles = []fileChange{
		{status: "M", path: "logo.png"},
	}
	tab.fileSel = 0
	tab.imageDiff = &imageDiff{old: oldInfo, new: newInfo}

	a := &app{tabs: []*repoTab{tab}}
	tt := ui.NewTester(a.view, 1000, 700)
	tt.Frame()

	// 验证新旧版本标题与尺寸渲染
	if !tt.HasText("原版本") || !tt.HasText("新版本") {
		t.Fatalf("应该包含原版本和新版本，当前文本: %q", tt.Texts())
	}
	if !tt.HasText("50 × 50") || !tt.HasText("100 × 100") {
		t.Fatalf("应该包含分辨率信息，当前文本: %q", tt.Texts())
	}
	if !tt.HasText("并排") || !tt.HasText("自适应") {
		t.Fatalf("应该包含控制按钮，当前文本: %q", tt.Texts())
	}

	// 切换至 1:1 实际尺寸
	if err := tt.Click("自适应"); err != nil {
		t.Fatalf("点击自适应失败: %v", err)
	}
	tt.Frame()
	if !tab.imageFitActual || !tt.HasText("1:1 尺寸") {
		t.Fatalf("点击后应该切换至 1:1 尺寸")
	}

	// 切换至仅新版视图
	if err := tt.Click("新版"); err != nil {
		t.Fatalf("点击新版失败: %v", err)
	}
	tt.Frame()
	if tab.imageViewMode != 1 {
		t.Fatalf("viewMode 应该为 1，实际: %d", tab.imageViewMode)
	}

	// 测试新增单图渲染
	tab.imageDiff = &imageDiff{new: newInfo}
	tab.compareFiles[0].status = "A"
	tt.Frame()
	if !tt.HasText("新增图片") || !tt.HasText("新增") {
		t.Fatalf("新增图片应该显示新增标签，当前文本: %q", tt.Texts())
	}

	// 测试完整文件标签页单图渲染
	tab.fileTabs = []*fileDiffTab{
		{
			id:        1,
			file:      fileChange{path: "logo.png"},
			fullFile:  true,
			fullRev:   "HEAD",
			fullImage: newInfo,
		},
	}
	tab.contentTab = 1
	tt.Frame()
	if !tt.HasText("logo.png") || !tt.HasText("HEAD") {
		t.Fatalf("完整文件标签页应显示文件名与版本，当前文本: %q", tt.Texts())
	}
}

func TestGitWorktreeImageChange(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		if out, err := runGit(context.Background(), dir, args...); err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, out)
		}
	}

	run("init", "-b", "main")
	run("config", "user.name", "Test")
	run("config", "user.email", "test@test.com")

	img1 := makeTestPNG(40, 40)
	img2 := makeTestPNG(80, 80)

	imgPath := filepath.Join(dir, "app.png")
	if err := os.WriteFile(imgPath, img1, 0o644); err != nil {
		t.Fatal(err)
	}

	// 检查未跟踪状态下的图片识别
	files, err := worktreeStatus(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].path != "app.png" || !files[0].binary {
		t.Fatalf("未跟踪的图片应标记为 binary: %+v", files)
	}

	// 提交初版图片
	run("add", "app.png")
	run("commit", "-m", "add app.png")
	headHash, err := resolveCommit(context.Background(), dir, "HEAD")
	if err != nil {
		t.Fatal(err)
	}

	// 覆盖图片形成工作区改动
	if err := os.WriteFile(imgPath, img2, 0o644); err != nil {
		t.Fatal(err)
	}

	// 检查工作区对比读取与 prepare
	fChange := fileChange{path: "app.png", status: "M"}
	source, err := readFileDiffSource(context.Background(), dir, hashHex(headHash), "", fChange, true)
	if err != nil {
		t.Fatalf("readFileDiffSource failed: %v", err)
	}
	if len(source.old) != len(img1) || len(source.new) != len(img2) {
		t.Fatalf("source sizes mismatch: old=%d new=%d", len(source.old), len(source.new))
	}

	diff, simple, imgDiff, note, err := source.prepare(context.Background(), fChange.path, false)
	if err != nil {
		t.Fatalf("prepare failed: %v", err)
	}
	if diff != nil || simple != nil || note != "" {
		t.Fatalf("image prepare shouldn't return text diff or note: diff=%v simple=%v note=%s", diff, simple, note)
	}
	if imgDiff == nil || imgDiff.old == nil || imgDiff.new == nil {
		t.Fatalf("expected both old and new imageInfo in imgDiff: %+v", imgDiff)
	}
	if imgDiff.old.width != 40 || imgDiff.new.width != 80 {
		t.Fatalf("wrong dimensions in imgDiff: old=%d new=%d", imgDiff.old.width, imgDiff.new.width)
	}
}
