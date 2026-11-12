package store

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

func openTestStore(t *testing.T) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "assetstudio.db"), dir)
	if err != nil {
		t.Fatalf("打开测试库失败: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, dir
}

// 加列时最容易漏掉的是 assetColumns 与 scanAsset 的参数列表：
// 两者一旦不同步，SELECT 会直接 Scan 失败，但只有真跑一次才会暴露。
func TestAssetColumnsMatchTable(t *testing.T) {
	s, _ := openTestStore(t)

	rows, err := s.db.Query("PRAGMA table_info(assets)")
	if err != nil {
		t.Fatalf("读表结构失败: %v", err)
	}
	defer rows.Close()

	var tableCols []string
	for rows.Next() {
		var cid int
		var name, ctype string
		var notNull, pk int
		var dflt any
		if err := rows.Scan(&cid, &name, &ctype, &notNull, &dflt, &pk); err != nil {
			t.Fatalf("读列定义失败: %v", err)
		}
		tableCols = append(tableCols, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("表结构遍历失败: %v", err)
	}

	want := append([]string{}, assetColumnList...)
	got := append([]string{}, tableCols...)
	sort.Strings(want)
	sort.Strings(got)
	if strings.Join(want, "|") != strings.Join(got, "|") {
		t.Fatalf("列清单与表结构不一致\ntable: %v\nlist : %v", tableCols, assetColumnList)
	}
	if len(assetColumnList) != 32 {
		t.Fatalf("列数应是 32，实际 %d（加列请同时更新 schemaMigrations）", len(assetColumnList))
	}
}

// 写进去的每个字段都要能原样读回来：这条用例是列顺序 / 参数顺序漂移的探针。
func TestCreateGetRoundTrip(t *testing.T) {
	s, _ := openTestStore(t)
	ctx := context.Background()

	in := &Asset{
		ID:            "a-roundtrip",
		Name:          "往返测试",
		Description:   "描述里有中文与换行\n第二行",
		Category:      "机械",
		Tags:          []string{"tag-a", "标签-b"},
		Format:        "glb",
		FileName:      "model.glb",
		FileSize:      432816,
		FilePath:      "models/a-roundtrip.glb",
		ThumbPath:     "thumbs/a-roundtrip.webp",
		SourcePath:    `D:\assets\model.glb`,
		SidecarDir:    "sidecars/a-roundtrip",
		CreatedAt:     1794377386103,
		UpdatedAt:     1794377386103,
		MetricsState:  "ok",
		MetricsSource: "server",
		ThumbState:    "ok",
		Attempts:      2,
		LastError:     "上一次失败的原因",
		SHA256:        "6f1a",
		Stats: Stats{
			Vertices:   1200,
			Triangles:  400,
			Meshes:     3,
			Materials:  2,
			Dimensions: [3]float64{3.32, 3.47, 1.64},
		},
	}
	if err := s.Create(ctx, in); err != nil {
		t.Fatalf("写入失败: %v", err)
	}

	out, err := s.Get(ctx, in.ID)
	if err != nil {
		t.Fatalf("读回失败: %v", err)
	}

	if out.Name != in.Name || out.Description != in.Description || out.Category != in.Category {
		t.Fatalf("文本字段没原样返回: %+v", out)
	}
	if strings.Join(out.Tags, ",") != strings.Join(in.Tags, ",") {
		t.Fatalf("标签丢了: %v", out.Tags)
	}
	if out.FileSize != in.FileSize || out.FilePath != in.FilePath || out.SourcePath != in.SourcePath {
		t.Fatalf("文件字段没原样返回: %+v", out)
	}
	if out.SidecarDir != in.SidecarDir || !out.HasSidecars {
		t.Fatalf("附属目录没原样返回: %q", out.SidecarDir)
	}
	if out.CreatedAt != in.CreatedAt || out.UpdatedAt != in.UpdatedAt {
		t.Fatalf("时间戳不一致: %d / %d", out.CreatedAt, out.UpdatedAt)
	}
	if out.Stats != in.Stats {
		t.Fatalf("指标不一致: %+v vs %+v", out.Stats, in.Stats)
	}
	if out.MetricsState != "ok" || out.MetricsSource != "server" || out.ThumbState != "ok" {
		t.Fatalf("状态字段不一致: %s / %s / %s", out.MetricsState, out.MetricsSource, out.ThumbState)
	}
	if out.Attempts != 2 || out.LastError != in.LastError || out.SHA256 != in.SHA256 {
		t.Fatalf("失败记录或哈希丢失: %d / %q / %q", out.Attempts, out.LastError, out.SHA256)
	}
	if out.FileName != in.FileName || out.Format != in.Format {
		t.Fatalf("文件名或格式不一致: %q / %q", out.FileName, out.Format)
	}
	if !out.HasThumb || out.ThumbPath == "" {
		t.Fatalf("缩略图标记丢了: %+v", out)
	}
}

// 同一个库连续打开两次：版本已到顶，不该再重放 DDL，也不该动已有数据。
func TestOpenIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "assetstudio.db")

	s, err := Open(path, dir)
	if err != nil {
		t.Fatalf("首次打开失败: %v", err)
	}
	ctx := context.Background()
	if err := s.Create(ctx, &Asset{ID: "a-keep", Name: "留存", Format: "obj", CreatedAt: time.Now().UnixMilli()}); err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	_ = s.Close()

	s2, err := Open(path, dir)
	if err != nil {
		t.Fatalf("二次打开失败: %v", err)
	}
	defer s2.Close()

	var version int
	if err := s2.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatalf("读版本失败: %v", err)
	}
	if version != schemaVersion {
		t.Fatalf("二次打开后版本应是 %d，实际 %d", schemaVersion, version)
	}
	if _, err := s2.Get(ctx, "a-keep"); err != nil {
		t.Fatalf("二次打开后数据丢了: %v", err)
	}
}

// 比程序更新的库必须拒绝打开：回退代码不能带着未知列继续跑。
func TestRejectsNewerSchema(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "assetstudio.db")

	s, err := Open(path, dir)
	if err != nil {
		t.Fatalf("首次打开失败: %v", err)
	}
	_ = s.Close()

	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("直连失败: %v", err)
	}
	if _, err := raw.Exec("PRAGMA user_version = 99"); err != nil {
		t.Fatalf("改版本失败: %v", err)
	}
	_ = raw.Close()

	if _, err := Open(path, dir); err == nil {
		t.Fatalf("v99 的库不该被打开")
	} else if !strings.Contains(err.Error(), "高于本程序支持") {
		t.Fatalf("报错文案不对: %v", err)
	}
}

// 分类统计按分类聚合，不是按资产名聚合：assets 表有一列也叫 name，
// GROUP BY 写别名会被解析成真实列，这条用例把它钉住。
func TestSummaryGroupsByCategory(t *testing.T) {
	s, _ := openTestStore(t)
	ctx := context.Background()

	rows := []struct {
		id, name, category string
	}{
		{"a-1", "甲", "机械"},
		{"a-2", "乙", "机械"},
		{"a-3", "丙", "自然"},
		{"a-4", "丁", ""},
		{"a-5", "戊", ""},
		{"a-6", "己", ""},
	}
	for _, r := range rows {
		if err := s.Create(ctx, &Asset{ID: r.id, Name: r.name, Category: r.category, Format: "obj"}); err != nil {
			t.Fatalf("写入 %s 失败: %v", r.id, err)
		}
	}

	sum, err := s.Summary(ctx)
	if err != nil {
		t.Fatalf("统计失败: %v", err)
	}
	if sum.Count != 6 {
		t.Fatalf("总数应是 6，实际 %d", sum.Count)
	}
	if len(sum.Categories) != 3 {
		t.Fatalf("应有 3 个分类，实际 %d: %+v", len(sum.Categories), sum.Categories)
	}
	want := map[string]int{"机械": 2, "自然": 1, "未分类": 3}
	for _, c := range sum.Categories {
		if want[c.Name] != c.Count {
			t.Fatalf("分类 %q 计数不对: %+v", c.Name, sum.Categories)
		}
	}
}

// placeholders 的数量必须与列数一致，否则 INSERT 参数个数对不上。
func TestPlaceholders(t *testing.T) {
	for _, n := range []int{1, 2, 32} {
		got := placeholders(n)
		if strings.Count(got, "?") != n {
			t.Fatalf("%d 列的占位符生成了 %q", n, got)
		}
	}
	if placeholders(0) != "" {
		t.Fatalf("0 列应返回空串")
	}
}

// 替换一次：旧版本进表，当前版本号 +1，新指标覆盖旧指标。
func TestApplyReplaceArchivesAndBumpsVersion(t *testing.T) {
	s, _ := openTestStore(t)
	ctx := context.Background()

	in := &Asset{
		ID: "a-replace", Name: "替换", Format: "obj", FileName: "old.obj",
		FileSize: 111, FilePath: "models/a-replace.obj",
		Stats:       Stats{Vertices: 8, Triangles: 12, Meshes: 1, Materials: 1, Dimensions: [3]float64{1, 2, 3}},
		CreatedAt:   1000,
		UpdatedAt:   2000,
		MetricsState: "ok", MetricsSource: "server", SHA256: "oldsum",
	}
	if err := s.Create(ctx, in); err != nil {
		t.Fatalf("写资产失败: %v", err)
	}

	updated, err := s.ApplyReplace(ctx, "a-replace", ReplaceInput{
		NewFileSize: 222, NewSHA256: "newsum",
		NewStats:     Stats{Vertices: 4, Triangles: 6, Meshes: 2, Materials: 3, Dimensions: [3]float64{4, 5, 6}},
		MetricsState: "ok", MetricsSource: "server", ThumbState: "missing",
		Old: AssetVersion{Version: 1, FileName: "old.obj", Format: "obj", FileSize: 111,
			SHA256: "oldsum", CreatedAt: 2000, FilePath: "versions/a-replace/v1.obj"},
		ReplacedAt: 3000,
	})
	if err != nil {
		t.Fatalf("替换失败: %v", err)
	}
	if updated.Version != 2 {
		t.Fatalf("当前版本应是 2，实际 %d", updated.Version)
	}
	if updated.FileSize != 222 || updated.SHA256 != "newsum" {
		t.Fatalf("当前文件字段没更新: %+v", updated)
	}
	if updated.Stats.Triangles != 6 || updated.Stats.Meshes != 2 || updated.Stats.Dimensions[2] != 6 {
		t.Fatalf("指标没更新: %+v", updated.Stats)
	}
	if updated.ThumbState != "missing" {
		t.Fatalf("换模型后缩略图应标记待生成，实际 %q", updated.ThumbState)
	}

	versions, err := s.Versions(ctx, "a-replace")
	if err != nil {
		t.Fatalf("读版本失败: %v", err)
	}
	if len(versions) != 1 || versions[0].Version != 1 {
		t.Fatalf("应有 1 条旧版本记录，实际 %+v", versions)
	}
	if versions[0].FilePath != "versions/a-replace/v1.obj" || versions[0].FileSize != 111 {
		t.Fatalf("旧版本记录不对: %+v", versions[0])
	}

	// 再替换一次：版本号继续 +1，列表按版本号倒序
	if _, err := s.ApplyReplace(ctx, "a-replace", ReplaceInput{
		NewFileSize: 333, NewSHA256: "sum3",
		NewStats:      Stats{Vertices: 1, Triangles: 1, Meshes: 1, Materials: 1},
		MetricsState:  "ok", MetricsSource: "server", ThumbState: "missing",
		Old:           AssetVersion{Version: 2, FileName: "v2.obj", Format: "obj", FileSize: 222, CreatedAt: 3000, FilePath: "versions/a-replace/v2.obj"},
		ReplacedAt:    4000,
	}); err != nil {
		t.Fatalf("第二次替换失败: %v", err)
	}
	versions, _ = s.Versions(ctx, "a-replace")
	if len(versions) != 2 || versions[0].Version != 2 || versions[1].Version != 1 {
		t.Fatalf("版本列表应倒序为 2,1，实际 %+v", versions)
	}
}

// PurgeFiles 要把版本目录一起挪进 trash，否则删资产后旧版本文件会留在 data/versions 里当垃圾。
func TestPurgeFilesMovesVersionsDir(t *testing.T) {
	s, dir := openTestStore(t)
	modelPath := filepath.Join(dir, "models", "a-purge.obj")
	versionPath := filepath.Join(s.VersionsDir, "a-purge", "v1.obj")
	for _, path := range []string{modelPath, versionPath} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("建目录失败: %v", err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatalf("写文件失败: %v", err)
		}
	}
	asset := &Asset{ID: "a-purge", FilePath: "models/a-purge.obj", Format: "obj"}
	if err := s.PurgeFiles(asset); err != nil {
		t.Fatalf("挪文件失败: %v", err)
	}
	if _, err := os.Stat(versionPath); !os.IsNotExist(err) {
		t.Fatalf("版本目录应已离开 data/versions，stat err=%v", err)
	}
	trashed := filepath.Join(s.TrashDir, "a-purge", "versions", "v1.obj")
	if _, err := os.Stat(trashed); err != nil {
		t.Fatalf("版本文件应进 trash: %v", err)
	}
}

// 删资产时版本记录必须一起走，否则 asset_versions 会留下指向已删文件的孤儿行。
func TestHardDeleteRemovesVersions(t *testing.T) {
	s, _ := openTestStore(t)
	ctx := context.Background()
	if err := s.Create(ctx, &Asset{ID: "a-del", Name: "删", Format: "obj", FilePath: "models/a-del.obj"}); err != nil {
		t.Fatalf("写资产失败: %v", err)
	}
	if _, err := s.ApplyReplace(ctx, "a-del", ReplaceInput{
		NewFileSize: 5, NewStats: Stats{}, MetricsState: "ok", MetricsSource: "server", ThumbState: "missing",
		Old:        AssetVersion{Version: 1, FileName: "a.obj", Format: "obj", FileSize: 4, FilePath: "versions/a-del/v1.obj"},
		ReplacedAt: 100,
	}); err != nil {
		t.Fatalf("替换失败: %v", err)
	}
	if err := s.HardDelete(ctx, "a-del"); err != nil {
		t.Fatalf("删行失败: %v", err)
	}
	versions, err := s.Versions(ctx, "a-del")
	if err != nil {
		t.Fatalf("读版本失败: %v", err)
	}
	if len(versions) != 0 {
		t.Fatalf("删资产后版本记录应为空，实际 %+v", versions)
	}
	if _, err := s.Get(ctx, "a-del"); err == nil {
		t.Fatalf("资产行应被删掉")
	}
}

// 回滚也是「一次替换」：被回滚掉的那一版照样进表，版本号只增不减。
func TestApplyReplaceCanRestoreThenRollForward(t *testing.T) {
	s, _ := openTestStore(t)
	ctx := context.Background()
	if err := s.Create(ctx, &Asset{ID: "a-restore", Name: "回滚", Format: "obj",
		FileSize: 10, FilePath: "models/a-restore.obj", CreatedAt: 1, UpdatedAt: 1}); err != nil {
		t.Fatalf("写资产失败: %v", err)
	}
	for i := 2; i <= 3; i++ {
		if _, err := s.ApplyReplace(ctx, "a-restore", ReplaceInput{
			NewFileSize: int64(i * 10), NewSHA256: "sum",
			NewStats:     Stats{Vertices: i, Triangles: i, Meshes: 1},
			MetricsState: "ok", MetricsSource: "server", ThumbState: "missing",
			Old: AssetVersion{Version: i - 1, FileName: "prev.obj", Format: "obj",
				FileSize: 10, CreatedAt: int64(i),
				FilePath: filepath.ToSlash(filepath.Join("versions", "a-restore", "v"+strconv.Itoa(i-1)+".obj"))},
			ReplacedAt: int64(i * 100),
		}); err != nil {
			t.Fatalf("第 %d 次替换失败: %v", i, err)
		}
	}
	asset, err := s.Get(ctx, "a-restore")
	if err != nil {
		t.Fatalf("读资产失败: %v", err)
	}
	if asset.Version != 3 {
		t.Fatalf("当前版本应是 3，实际 %d", asset.Version)
	}
}
