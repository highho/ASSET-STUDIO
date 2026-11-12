package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

var ErrNotFound = errors.New("asset not found")

type Stats struct {
	Vertices   int        `json:"vertices"`
	Triangles  int        `json:"triangles"`
	Meshes     int        `json:"meshes"`
	Materials  int        `json:"materials"`
	Dimensions [3]float64 `json:"dimensions"`
}

// AIResult 是 AI 智能分类的落点：数据库与接口已预留，接入模型后只需填充这里。
type AIResult struct {
	Category   string   `json:"category"`
	Confidence float64  `json:"confidence"`
	Tags       []string `json:"tags"`
	Provider   string   `json:"provider,omitempty"`
}

type Asset struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Category    string    `json:"category"`
	Tags        []string  `json:"tags"`
	Format      string    `json:"format"`
	FileName    string    `json:"fileName"`
	FileSize    int64     `json:"fileSize"`
	Version     int       `json:"version"`
	Stats       Stats     `json:"stats"`
	CreatedAt   int64     `json:"createdAt"`
	UpdatedAt   int64     `json:"updatedAt"`
	SourcePath  string    `json:"sourcePath,omitempty"`
	AI          *AIResult `json:"ai,omitempty"`
	HasThumb    bool      `json:"hasThumb"`
	HasSidecars bool      `json:"hasSidecars"`

	// 指标与缩略图的状态：服务端解析后即为 ok/server；FBX 之类留给浏览器的是 pending/client。
	MetricsState  string `json:"metricsState"`
	MetricsSource string `json:"metricsSource"`
	ThumbState    string `json:"thumbState"`
	Attempts      int    `json:"attempts"`
	LastError     string `json:"lastError,omitempty"`
	SHA256        string `json:"sha256,omitempty"`
	DeletedAt     int64  `json:"-"`

	FilePath   string `json:"-"`
	ThumbPath  string `json:"-"`
	SidecarDir string `json:"-"`
}

type CategoryCount struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

type Summary struct {
	Count      int             `json:"count"`
	TotalBytes int64           `json:"totalBytes"`
	Categories []CategoryCount `json:"categories"`
}

type Store struct {
	db          *sql.DB
	DataDir     string
	ModelsDir   string
	ThumbsDir   string
	SidecarsDir string
	VersionsDir string
	TrashDir    string
}

// assetColumnList 是 assets 表的完整列清单，顺序即 SELECT 的顺序。
// 加列时这三处必须同步：本列表、scanAsset 的 Scan 参数、Create 的 INSERT 参数。
// store_test.go 的 TestAssetColumnsMatchTable 与 TestCreateGetRoundTrip 会在
// 漂移时立刻失败 —— 这是唯一的强制点，别只改一半。
var assetColumnList = []string{
	"id", "name", "description", "category", "tags", "format", "file_name", "file_size",
	"file_path", "thumb_path", "vertices", "triangles", "meshes", "materials",
	"dim_x", "dim_y", "dim_z", "source_path", "ai_category", "ai_confidence", "ai_tags",
	"created_at", "updated_at", "sidecar_dir",
	"metrics_state", "metrics_source", "thumb_state", "attempts", "last_error", "sha256", "deleted_at",
	"version",
}

// assetColumns 是 SELECT 用的列串，从上面的列表拼出来，避免两处各写一遍。
var assetColumns = strings.Join(assetColumnList, ", ")

const schema = `
CREATE TABLE IF NOT EXISTS assets (
	id            TEXT PRIMARY KEY,
	name          TEXT NOT NULL,
	description   TEXT NOT NULL DEFAULT '',
	category      TEXT NOT NULL DEFAULT '',
	tags          TEXT NOT NULL DEFAULT '[]',
	format        TEXT NOT NULL,
	file_name     TEXT NOT NULL,
	file_size     INTEGER NOT NULL DEFAULT 0,
	file_path     TEXT NOT NULL,
	thumb_path    TEXT NOT NULL DEFAULT '',
	vertices      INTEGER NOT NULL DEFAULT 0,
	triangles     INTEGER NOT NULL DEFAULT 0,
	meshes        INTEGER NOT NULL DEFAULT 0,
	materials     INTEGER NOT NULL DEFAULT 0,
	dim_x         REAL NOT NULL DEFAULT 0,
	dim_y         REAL NOT NULL DEFAULT 0,
	dim_z         REAL NOT NULL DEFAULT 0,
	source_path   TEXT NOT NULL DEFAULT '',
	sidecar_dir   TEXT NOT NULL DEFAULT '',
	ai_category   TEXT NOT NULL DEFAULT '',
	ai_confidence REAL NOT NULL DEFAULT 0,
	ai_tags       TEXT NOT NULL DEFAULT '[]',
	created_at    INTEGER NOT NULL,
	updated_at    INTEGER NOT NULL,
	metrics_state  TEXT NOT NULL DEFAULT 'pending',
	metrics_source TEXT NOT NULL DEFAULT '',
	thumb_state    TEXT NOT NULL DEFAULT 'missing',
	attempts       INTEGER NOT NULL DEFAULT 0,
	last_error     TEXT NOT NULL DEFAULT '',
	sha256         TEXT NOT NULL DEFAULT '',
	deleted_at     INTEGER NOT NULL DEFAULT 0,
	version        INTEGER NOT NULL DEFAULT 1
);
CREATE INDEX IF NOT EXISTS idx_assets_created ON assets(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_assets_category ON assets(category);
CREATE INDEX IF NOT EXISTS idx_assets_source ON assets(source_path);
`

func Open(dbPath, dataDir string) (*Store, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, fmt.Errorf("创建数据目录失败: %w", err)
	}
	s := &Store{
		DataDir:     dataDir,
		ModelsDir:   filepath.Join(dataDir, "models"),
		ThumbsDir:   filepath.Join(dataDir, "thumbs"),
		SidecarsDir: filepath.Join(dataDir, "sidecars"),
		VersionsDir: filepath.Join(dataDir, "versions"),
		TrashDir:    filepath.Join(dataDir, "trash"),
	}
	for _, dir := range []string{s.ModelsDir, s.ThumbsDir, s.SidecarsDir, s.VersionsDir, s.TrashDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("创建存储目录失败: %w", err)
		}
	}

	// 每条连接都要自带 WAL 与 busy_timeout：连接池里的连接是懒创建的，
	// 只在初始那条上 Exec 一次 PRAGMA，后来的连接就没有超时重试了。
	dsn := dbPath + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开数据库失败: %w", err)
	}
	// 读可以并发（WAL 下读不互相阻塞），写仍由 SQLite 串行化，busy_timeout 负责排队重试。
	// 原来设成 1 是把「写要排队」扩大成了「读也要排队」。
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(4)
	// 建表也在迁移阶梯里（v1），这里只负责把库升到当前版本。
	if err := migrateSchema(db); err != nil {
		// 打不开就别把连接留着：Windows 下句柄不释放，文件会一直被占住
		_ = db.Close()
		return nil, fmt.Errorf("升级表结构失败: %w", err)
	}
	s.db = db
	return s, nil
}

// migrateSchema 按 PRAGMA user_version 逐级升级表结构。
// 老库不用重建：每一级都只在「当前版本还没到」时才跑，且内部都带幂等判断
// （列已存在就跳过、索引 IF NOT EXISTS），所以半路失败的库再启动一次也能续上。
func migrateSchema(db *sql.DB) error {
	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("读取数据库版本失败: %w", err)
	}
	if version > schemaVersion {
		return fmt.Errorf("数据库版本 v%d 高于本程序支持的 v%d，请用新版服务打开", version, schemaVersion)
	}
	for _, step := range schemaMigrations {
		if version >= step.version {
			continue
		}
		if err := step.apply(db); err != nil {
			return fmt.Errorf("升级到 v%d（%s）失败: %w", step.version, step.name, err)
		}
		if _, err := db.Exec(fmt.Sprintf("PRAGMA user_version = %d", step.version)); err != nil {
			return fmt.Errorf("记录数据库版本 v%d 失败: %w", step.version, err)
		}
	}
	return nil
}

// schemaVersion 是当前表结构的版本。改表结构时：加一条 migration，并把这里 +1。
const schemaVersion = 4

type migration struct {
	version int
	name    string
	apply   func(*sql.DB) error
}

// schemaMigrations 是版本阶梯。新库从 0 一路跑到 schemaVersion，
// 老库只跑自己缺的那几级。
var schemaMigrations = []migration{
	{1, "建表与基础索引", func(db *sql.DB) error {
		_, err := db.Exec(schema)
		return err
	}},
	{2, "补指标、缩略图、哈希与墓碑列", addMissingColumns},
	{3, "来源路径唯一索引与缩略图状态回填", finalizeIndexes},
	{4, "版本列与版本表", addVersions},
}

// addMissingColumns 给老库补列。v1 的 schema 已经带全了，所以新库这里全是空转。
func addMissingColumns(db *sql.DB) error {
	columns := []struct {
		name string
		ddl  string
	}{
		{"sidecar_dir", `ALTER TABLE assets ADD COLUMN sidecar_dir TEXT NOT NULL DEFAULT ''`},
		{"metrics_state", `ALTER TABLE assets ADD COLUMN metrics_state TEXT NOT NULL DEFAULT 'pending'`},
		{"metrics_source", `ALTER TABLE assets ADD COLUMN metrics_source TEXT NOT NULL DEFAULT ''`},
		{"thumb_state", `ALTER TABLE assets ADD COLUMN thumb_state TEXT NOT NULL DEFAULT 'missing'`},
		{"attempts", `ALTER TABLE assets ADD COLUMN attempts INTEGER NOT NULL DEFAULT 0`},
		{"last_error", `ALTER TABLE assets ADD COLUMN last_error TEXT NOT NULL DEFAULT ''`},
		{"sha256", `ALTER TABLE assets ADD COLUMN sha256 TEXT NOT NULL DEFAULT ''`},
		{"deleted_at", `ALTER TABLE assets ADD COLUMN deleted_at INTEGER NOT NULL DEFAULT 0`},
	}
	for _, column := range columns {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('assets') WHERE name = ?`, column.name).Scan(&count); err != nil {
			return err
		}
		if count == 0 {
			if _, err := db.Exec(column.ddl); err != nil {
				return err
			}
		}
	}
	return nil
}

// finalizeIndexes 建唯一索引，并把老库里已有缩略图的状态补齐。
func finalizeIndexes(db *sql.DB) error {
	// 同一个来源路径只允许入库一次；按小写比较，避免 Windows 下大小写不同算成两条。
	// 墓碑行不参与唯一性：删除失败留下的墓碑不该挡住重新导入同一条路径。
	// 老库里若已存在重复，索引建不起来 —— 此时保留普通索引，不让服务启动失败。
	_, _ = db.Exec(`DROP INDEX IF EXISTS idx_assets_source_unique`)
	_, _ = db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_assets_source_unique
		ON assets(lower(source_path)) WHERE source_path <> '' AND deleted_at = 0`)

	// 老库里已有的缩略图按实际记录补齐状态，避免全部显示成「待生成」。
	_, _ = db.Exec(`UPDATE assets SET thumb_state = 'ok' WHERE thumb_path <> '' AND thumb_state = 'missing'`)
	return nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

// addVersions 给 assets 补 version 列，并建版本表。
// 版本表只记「已经被替换掉的那一版」：当前文件始终留在 models/<id>.<ext>，
// 被换下来的旧文件挪进 versions/<id>/v<n>.<ext>，回滚就是把旧文件再放回去。
func addVersions(db *sql.DB) error {
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('assets') WHERE name = 'version'`).Scan(&count); err != nil {
		return err
	}
	if count == 0 {
		if _, err := db.Exec(`ALTER TABLE assets ADD COLUMN version INTEGER NOT NULL DEFAULT 1`); err != nil {
			return err
		}
	}
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS asset_versions (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			asset_id   TEXT NOT NULL,
			version    INTEGER NOT NULL,
			file_path  TEXT NOT NULL,
			file_name  TEXT NOT NULL,
			format     TEXT NOT NULL,
			file_size  INTEGER NOT NULL DEFAULT 0,
			sha256     TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL,
			replaced_at INTEGER NOT NULL
		)`); err != nil {
		return err
	}
	_, err := db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_versions_asset ON asset_versions(asset_id, version)`)
	return err
}

func NewID(prefix string) string {
	buf := make([]byte, 6)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%s-%d", prefix, time.Now().UnixMilli())
	}
	return fmt.Sprintf("%s-%d-%s", prefix, time.Now().UnixMilli(), hex.EncodeToString(buf))
}

func encodeTags(tags []string) string {
	if tags == nil {
		return "[]"
	}
	raw, err := json.Marshal(tags)
	if err != nil {
		return "[]"
	}
	return string(raw)
}

func decodeTags(raw string) []string {
	if raw == "" {
		return []string{}
	}
	var tags []string
	if err := json.Unmarshal([]byte(raw), &tags); err != nil || tags == nil {
		return []string{}
	}
	return tags
}

func scanAsset(rows *sql.Rows) (*Asset, error) {
	var a Asset
	var tagsRaw, aiTagsRaw, aiCategory, thumb, sidecar string
	var aiConfidence float64

	err := rows.Scan(
		&a.ID, &a.Name, &a.Description, &a.Category, &tagsRaw, &a.Format, &a.FileName, &a.FileSize,
		&a.FilePath, &thumb,
		&a.Stats.Vertices, &a.Stats.Triangles, &a.Stats.Meshes, &a.Stats.Materials,
		&a.Stats.Dimensions[0], &a.Stats.Dimensions[1], &a.Stats.Dimensions[2],
		&a.SourcePath, &aiCategory, &aiConfidence, &aiTagsRaw,
		&a.CreatedAt, &a.UpdatedAt, &sidecar,
		&a.MetricsState, &a.MetricsSource, &a.ThumbState, &a.Attempts, &a.LastError, &a.SHA256, &a.DeletedAt,
		&a.Version,
	)
	if err != nil {
		return nil, err
	}
	a.Tags = decodeTags(tagsRaw)
	a.ThumbPath = thumb
	a.HasThumb = thumb != ""
	a.SidecarDir = sidecar
	a.HasSidecars = sidecar != ""
	if aiCategory != "" {
		a.AI = &AIResult{
			Category:   aiCategory,
			Confidence: aiConfidence,
			Tags:       decodeTags(aiTagsRaw),
		}
	}
	return &a, nil
}

func (s *Store) List(ctx context.Context) ([]Asset, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+assetColumns+" FROM assets WHERE deleted_at = 0 ORDER BY created_at DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	assets := make([]Asset, 0, 64)
	for rows.Next() {
		asset, err := scanAsset(rows)
		if err != nil {
			return nil, err
		}
		assets = append(assets, *asset)
	}
	return assets, rows.Err()
}

func (s *Store) Get(ctx context.Context, id string) (*Asset, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+assetColumns+" FROM assets WHERE id = ? AND deleted_at = 0", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, ErrNotFound
	}
	return scanAsset(rows)
}

func (s *Store) FindBySourcePath(ctx context.Context, sourcePath string) (*Asset, error) {
	if strings.TrimSpace(sourcePath) == "" {
		return nil, ErrNotFound
	}
	rows, err := s.db.QueryContext(ctx,
		"SELECT "+assetColumns+" FROM assets WHERE deleted_at = 0 AND lower(source_path) = lower(?) LIMIT 1", sourcePath)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, ErrNotFound
	}
	return scanAsset(rows)
}

func (s *Store) Create(ctx context.Context, a *Asset) error {
	if a.MetricsState == "" {
		a.MetricsState = "pending"
	}
	if a.ThumbState == "" {
		a.ThumbState = "missing"
	}
	if a.Version <= 0 {
		a.Version = 1
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO assets (`+assetColumns+`) VALUES (`+placeholders(len(assetColumnList))+`)`,
		a.ID, a.Name, a.Description, a.Category, encodeTags(a.Tags), a.Format, a.FileName, a.FileSize,
		a.FilePath, a.ThumbPath,
		a.Stats.Vertices, a.Stats.Triangles, a.Stats.Meshes, a.Stats.Materials,
		a.Stats.Dimensions[0], a.Stats.Dimensions[1], a.Stats.Dimensions[2],
		a.SourcePath, "", 0.0, "[]",
		a.CreatedAt, a.UpdatedAt, a.SidecarDir,
		a.MetricsState, a.MetricsSource, a.ThumbState, a.Attempts, a.LastError, a.SHA256, 0,
		a.Version,
	)
	return err
}

// placeholders 生成 "?, ?, ?"，数量与列数一致。
func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.Repeat("?, ", n-1) + "?"
}

func (s *Store) UpdateMeta(ctx context.Context, id, name, description, category string, tags []string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE assets SET name = ?, description = ?, category = ?, tags = ?, updated_at = ? WHERE id = ?`,
		name, description, category, encodeTags(tags), time.Now().UnixMilli(), id)
	if err != nil {
		return err
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateMetrics 接收浏览器回填的指标（FBX 等），来源记为 client。
func (s *Store) UpdateMetrics(ctx context.Context, id string, stats Stats) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE assets SET vertices = ?, triangles = ?, meshes = ?, materials = ?,
			dim_x = ?, dim_y = ?, dim_z = ?, metrics_state = 'ok', metrics_source = 'client',
			last_error = '', updated_at = ? WHERE id = ? AND deleted_at = 0`,
		stats.Vertices, stats.Triangles, stats.Meshes, stats.Materials,
		stats.Dimensions[0], stats.Dimensions[1], stats.Dimensions[2],
		time.Now().UnixMilli(), id)
	if err != nil {
		return err
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

// SetServerMetrics 落服务端解析出的指标；落下去之后这份数据就是权威，浏览器回填不再覆盖。
func (s *Store) SetServerMetrics(ctx context.Context, id string, stats Stats, sha256 string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE assets SET vertices = ?, triangles = ?, meshes = ?, materials = ?,
			dim_x = ?, dim_y = ?, dim_z = ?, sha256 = ?, metrics_state = 'ok', metrics_source = 'server',
			last_error = '', updated_at = ? WHERE id = ? AND deleted_at = 0`,
		stats.Vertices, stats.Triangles, stats.Meshes, stats.Materials,
		stats.Dimensions[0], stats.Dimensions[1], stats.Dimensions[2], sha256,
		time.Now().UnixMilli(), id)
	if err != nil {
		return err
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

// SetMetricsError 记录一次解析失败，供前端展示「为什么没有面数」而不是一片 0。
func (s *Store) SetMetricsError(ctx context.Context, id, message string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE assets SET metrics_state = 'error', metrics_source = 'server', last_error = ?,
			attempts = attempts + 1, updated_at = ? WHERE id = ? AND deleted_at = 0`,
		message, time.Now().UnixMilli(), id)
	return err
}

func (s *Store) SetThumb(ctx context.Context, id, thumbPath string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE assets SET thumb_path = ?, thumb_state = 'ok', attempts = 0, last_error = '',
			updated_at = ? WHERE id = ? AND deleted_at = 0`,
		thumbPath, time.Now().UnixMilli(), id)
	if err != nil {
		return err
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

// SetThumbError 由客户端上报一次缩略图生成失败，attempts 自增，前端据此停止无限重试。
func (s *Store) SetThumbError(ctx context.Context, id, message string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE assets SET thumb_state = 'error', last_error = ?, attempts = attempts + 1,
			updated_at = ? WHERE id = ? AND deleted_at = 0`,
		message, time.Now().UnixMilli(), id)
	return err
}

func (s *Store) SetAI(ctx context.Context, id string, result AIResult) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE assets SET ai_category = ?, ai_confidence = ?, ai_tags = ? WHERE id = ?`,
		result.Category, result.Confidence, encodeTags(result.Tags), id)
	return err
}

// MarkDeleted 先立墓碑：列表、详情、下载立刻看不到它，但库行还在，文件也还没动。
// 这样「删库行」和「删文件」不必挤在一个成功/失败二选一的动作里。
func (s *Store) MarkDeleted(ctx context.Context, id string) error {
	now := time.Now().UnixMilli()
	res, err := s.db.ExecContext(ctx,
		`UPDATE assets SET deleted_at = ?, updated_at = ? WHERE id = ? AND deleted_at = 0`, now, now, id)
	if err != nil {
		return err
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

// HardDelete 删除库行；调用前必须先 PurgeFiles 成功。
// 版本记录一并删：文件已经进了 trash，留着行只会在下次同 id 复用（不会发生）时变成孤儿。
func (s *Store) HardDelete(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM asset_versions WHERE asset_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM assets WHERE id = ?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

// PurgeFiles 把资产的文件挪进 data/trash/<id>/，挪不动就返回错误（调用方保留墓碑，重启后重试）。
// 只做改名不做复制：模型、缩略图、外部资源都在同一个 data 目录下，跨卷的情况本就不该出现。
func (s *Store) PurgeFiles(a *Asset) error {
	trashDir := filepath.Join(s.TrashDir, a.ID)
	if err := os.MkdirAll(trashDir, 0o755); err != nil {
		return err
	}
	var firstErr error
	move := func(rel, dstName string) {
		if rel == "" {
			return
		}
		src := filepath.Join(s.DataDir, rel)
		if _, err := os.Stat(src); err != nil {
			if os.IsNotExist(err) {
				return
			}
			if firstErr == nil {
				firstErr = err
			}
			return
		}
		dst := filepath.Join(trashDir, dstName)
		if err := os.Rename(src, dst); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	move(a.FilePath, "model"+filepath.Ext(a.FilePath))
	move(a.ThumbPath, "thumb"+filepath.Ext(a.ThumbPath))
	move(a.SidecarDir, "sidecars")
	// 旧版本整目录一起挪走：删资产时不必逐版判断，回滚能力随资产一起进 trash。
	versionsDir := filepath.Join(s.VersionsDir, a.ID)
	if _, err := os.Stat(versionsDir); err == nil {
		move(s.RelativePath(versionsDir), "versions")
	}
	return firstErr
}

// ReconcileReport 是启动对账的结果，只用于日志。
type ReconcileReport struct {
	Tombstones   int
	Purged       int
	Failed       int
	TrashCleaned int
}

// Reconcile 在启动时收尾没删干净的资产：墓碑行 → 挪文件 → 删行 → 清 trash。
// 上一次进程如果在中间被杀，这里能把状态推到底，不会留下「行没了文件还在」的孤儿。
func (s *Store) Reconcile(ctx context.Context) (ReconcileReport, error) {
	var report ReconcileReport

	rows, err := s.db.QueryContext(ctx, "SELECT "+assetColumns+" FROM assets WHERE deleted_at <> 0")
	if err != nil {
		return report, err
	}
	pending := make([]Asset, 0, 8)
	for rows.Next() {
		asset, scanErr := scanAsset(rows)
		if scanErr != nil {
			_ = rows.Close()
			return report, scanErr
		}
		pending = append(pending, *asset)
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return report, err
	}

	report.Tombstones = len(pending)
	for index := range pending {
		asset := &pending[index]
		if err := s.PurgeFiles(asset); err != nil {
			report.Failed++
			continue
		}
		if err := s.HardDelete(ctx, asset.ID); err != nil {
			report.Failed++
			continue
		}
		_ = os.RemoveAll(filepath.Join(s.TrashDir, asset.ID))
		report.Purged++
	}

	// trash 里剩下的都是「用户已经要求删除」的资产：行已删或已墓碑，文件属于垃圾。
	entries, err := os.ReadDir(s.TrashDir)
	if err != nil {
		return report, err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(s.TrashDir, entry.Name())
		if err := os.RemoveAll(path); err == nil {
			report.TrashCleaned++
		}
	}
	return report, nil
}

func (s *Store) Summary(ctx context.Context) (Summary, error) {
	var summary Summary
	if err := s.db.QueryRowContext(ctx,
		"SELECT COUNT(*), COALESCE(SUM(file_size), 0) FROM assets WHERE deleted_at = 0").
		Scan(&summary.Count, &summary.TotalBytes); err != nil {
		return summary, err
	}

	// 这里必须写完整表达式，不能写别名 name：assets 表本身有一列叫 name，
	// SQLite 在 GROUP BY 里会优先解析成真实列，结果变成「按资产名分组」，
	// 每个资产各成一行 —— 曾经就是这样，扫描 2000 个文件后分类统计全是 count=1。
	rows, err := s.db.QueryContext(ctx,
		`SELECT CASE WHEN category = '' THEN '未分类' ELSE category END AS cat, COUNT(*)
		 FROM assets WHERE deleted_at = 0
		 GROUP BY CASE WHEN category = '' THEN '未分类' ELSE category END
		 ORDER BY COUNT(*) DESC, cat ASC`)
	if err != nil {
		return summary, err
	}
	defer rows.Close()

	summary.Categories = make([]CategoryCount, 0, 8)
	for rows.Next() {
		var c CategoryCount
		if err := rows.Scan(&c.Name, &c.Count); err != nil {
			return summary, err
		}
		summary.Categories = append(summary.Categories, c)
	}
	return summary, rows.Err()
}

func (s *Store) AbsModelPath(id, format string) string {
	return filepath.Join(s.ModelsDir, id+"."+strings.ToLower(format))
}

func (s *Store) AbsThumbPath(id, ext string) string {
	return filepath.Join(s.ThumbsDir, id+"."+strings.ToLower(ext))
}

// AbsSidecarDir 是模型外部资源（.bin、贴图、.mtl）的落点目录。
func (s *Store) AbsSidecarDir(id string) string {
	return filepath.Join(s.SidecarsDir, id)
}

// AssetVersion 是一份「已经被替换掉」的旧主文件。当前这一版不在表里，它就是资产本身。
type AssetVersion struct {
	Version    int    `json:"version"`
	FileName   string `json:"fileName"`
	Format     string `json:"format"`
	FileSize   int64  `json:"fileSize"`
	SHA256     string `json:"sha256,omitempty"`
	CreatedAt  int64  `json:"createdAt"`
	ReplacedAt int64  `json:"replacedAt"`
	FilePath   string `json:"-"`
}

// VersionAbsPath 是某一版旧文件的位置：data/versions/<id>/v<n>.<ext>。
func (s *Store) VersionAbsPath(id string, version int, format string) string {
	return filepath.Join(s.VersionsDir, id, fmt.Sprintf("v%d.%s", version, strings.ToLower(format)))
}

// Versions 按版本号倒序列出旧版本（最新被换下来的在最前）。
func (s *Store) Versions(ctx context.Context, id string) ([]AssetVersion, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT version, file_name, format, file_size, sha256, created_at, replaced_at, file_path
		 FROM asset_versions WHERE asset_id = ? ORDER BY version DESC`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	versions := make([]AssetVersion, 0, 8)
	for rows.Next() {
		var v AssetVersion
		if err := rows.Scan(&v.Version, &v.FileName, &v.Format, &v.FileSize, &v.SHA256,
			&v.CreatedAt, &v.ReplacedAt, &v.FilePath); err != nil {
			return nil, err
		}
		versions = append(versions, v)
	}
	return versions, rows.Err()
}

// ReplaceInput 是一次替换里数据库要知道的全部事实。文件搬动由调用方先做，
// 这里只负责「记下旧版本 + 更新当前这一行」，两边在同一个事务里落定。
type ReplaceInput struct {
	NewFileSize   int64
	NewSHA256     string
	NewStats      Stats
	MetricsState  string
	MetricsSource string
	LastError     string
	ThumbState    string
	Old           AssetVersion
	ReplacedAt    int64
}

// ApplyReplace 在一个事务里写入版本记录并更新当前资产。失败时文件层由调用方回滚。
func (s *Store) ApplyReplace(ctx context.Context, id string, in ReplaceInput) (*Asset, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	old := in.Old
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO asset_versions (asset_id, version, file_path, file_name, format, file_size, sha256, created_at, replaced_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, old.Version, old.FilePath, old.FileName, old.Format, old.FileSize, old.SHA256,
		old.CreatedAt, in.ReplacedAt); err != nil {
		return nil, err
	}

	res, err := tx.ExecContext(ctx,
		`UPDATE assets SET file_size = ?, sha256 = ?, vertices = ?, triangles = ?, meshes = ?, materials = ?,
			dim_x = ?, dim_y = ?, dim_z = ?, metrics_state = ?, metrics_source = ?, last_error = ?,
			thumb_state = ?, attempts = 0, updated_at = ?, version = version + 1
		 WHERE id = ? AND deleted_at = 0`,
		in.NewFileSize, in.NewSHA256,
		in.NewStats.Vertices, in.NewStats.Triangles, in.NewStats.Meshes, in.NewStats.Materials,
		in.NewStats.Dimensions[0], in.NewStats.Dimensions[1], in.NewStats.Dimensions[2],
		in.MetricsState, in.MetricsSource, in.LastError, in.ThumbState, in.ReplacedAt, id)
	if err != nil {
		return nil, err
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return nil, ErrNotFound
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.Get(ctx, id)
}

// RelativePath 用于入库存相对路径，便于整个 data 目录搬迁。
func (s *Store) RelativePath(absPath string) string {
	return filepath.ToSlash(strings.TrimPrefix(absPath, s.DataDir+string(os.PathSeparator)))
}
