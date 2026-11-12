// Package job 提供一个极简的后台作业模型：长时间运行的批量操作（扫描、补齐、转换）
// 不再占用一次 HTTP 请求，而是登记成一个可查询进度、可取消的作业。
//
// 设计约束（单机单用户场景）：
//   - 只有一个进程，作业状态放内存即可，重启后消失 —— 需要持久化的结果已经落在库里；
//   - 同一类作业同时只允许一个，避免两路扫描互相抢磁盘；后来的请求拿到正在跑的那一个；
//   - 进度与失败明细有上限，2000 个文件扫失败也不会把内存撑爆。
package job

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"sync"
	"time"
)

// Status 作业状态。
type Status string

const (
	StatusRunning  Status = "running"
	StatusDone     Status = "done"
	StatusFailed   Status = "failed"
	StatusCanceled Status = "canceled"
)

// ErrRunning 表示同类作业已经在跑。返回的作业可以继续查询进度。
var ErrRunning = errors.New("同类作业正在运行")

// Failure 单条失败明细。只保留前 maxFailures 条。
type Failure struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// maxFailures 是失败明细的上限：够用户看清主要问题，又不会因为整盘不可读而吃光内存。
const maxFailures = 50

// Snapshot 是给 HTTP 层看的只读快照。
type Snapshot struct {
	ID        string          `json:"id"`
	Kind      string          `json:"kind"`
	Status    Status          `json:"status"`
	Total     int             `json:"total"`
	Done      int             `json:"done"`
	Skipped   int             `json:"skipped"`
	Failed    int             `json:"failed"`
	Message   string          `json:"message,omitempty"`
	Error     string          `json:"error,omitempty"`
	Failures  []Failure       `json:"failures,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`
	StartedAt int64           `json:"startedAt"`
	UpdatedAt int64           `json:"updatedAt"`
	EndedAt   int64           `json:"endedAt,omitempty"`
}

// Job 是一个正在运行的作业。方法都可以从作业 goroutine 里安全调用。
type Job struct {
	m    *Manager
	id   string
	kind string

	ctx    context.Context
	cancel context.CancelFunc

	status    Status
	total     int
	done      int
	skipped   int
	failed    int
	message   string
	errText   string
	failures  []Failure
	result    any
	startedAt int64
	updatedAt int64
	endedAt   int64
}

// ID 返回作业标识。
func (j *Job) ID() string { return j.id }

// Context 在作业被取消或服务退出时结束，循环里应定期检查。
func (j *Job) Context() context.Context { return j.ctx }

// SetTotal 设定总步数（先遍历再处理时，遍历完才知道总数）。
func (j *Job) SetTotal(total int) {
	j.m.mu.Lock()
	defer j.m.mu.Unlock()
	j.total = total
	j.updatedAt = time.Now().UnixMilli()
}

// Message 更新当前在做什么（前端显示文件名）。
func (j *Job) Message(text string) {
	j.m.mu.Lock()
	defer j.m.mu.Unlock()
	j.message = text
	j.updatedAt = time.Now().UnixMilli()
}

// Step 前进一格，可同时更新消息。
func (j *Job) Step(message string) {
	j.m.mu.Lock()
	defer j.m.mu.Unlock()
	j.done++
	if message != "" {
		j.message = message
	}
	j.updatedAt = time.Now().UnixMilli()
}

// Skip 记一条跳过（已导入过），同时前进一格。
func (j *Job) Skip(message string) {
	j.m.mu.Lock()
	defer j.m.mu.Unlock()
	j.done++
	j.skipped++
	if message != "" {
		j.message = message
	}
	j.updatedAt = time.Now().UnixMilli()
}

// Fail 记一条失败，同时前进一格。
func (j *Job) Fail(path, reason string) {
	j.m.mu.Lock()
	defer j.m.mu.Unlock()
	j.done++
	j.failed++
	if len(j.failures) < maxFailures {
		j.failures = append(j.failures, Failure{Path: path, Reason: reason})
	}
	j.updatedAt = time.Now().UnixMilli()
}

// Result 登记作业结果，完成后由快照返回给前端。
func (j *Job) Result(payload any) {
	j.m.mu.Lock()
	defer j.m.mu.Unlock()
	j.result = payload
}

// Manager 管理所有作业。零值不可用，用 NewManager 创建。
type Manager struct {
	mu     sync.Mutex
	jobs   map[string]*Job
	byKind map[string]*Job
	keep   int
	seq    int64
}

// NewManager 创建作业管理器，keep 是保留的已完成作业数量。
func NewManager(keep int) *Manager {
	if keep <= 0 {
		keep = 20
	}
	return &Manager{jobs: make(map[string]*Job), byKind: make(map[string]*Job), keep: keep}
}

// Start 启动一个作业。同类作业已在运行时返回 (当前作业, ErrRunning)。
// fn 返回 nil 视为完成，返回 context.Canceled 视为取消，其余视为失败。
func (m *Manager) Start(kind string, fn func(j *Job) error) (*Job, error) {
	m.mu.Lock()
	if existing := m.byKind[kind]; existing != nil && existing.status == StatusRunning {
		m.mu.Unlock()
		return existing, ErrRunning
	}
	m.seq++
	id := "j-" + time.Now().Format("150405") + "-" + strconv.FormatInt(m.seq, 10)
	ctx, cancel := context.WithCancel(context.Background())
	now := time.Now().UnixMilli()
	j := &Job{
		m: m, id: id, kind: kind, ctx: ctx, cancel: cancel,
		status: StatusRunning, startedAt: now, updatedAt: now,
	}
	m.jobs[id] = j
	m.byKind[kind] = j
	m.pruneLocked()
	m.mu.Unlock()

	go func() {
		err := fn(j)
		m.mu.Lock()
		defer m.mu.Unlock()
		j.endedAt = time.Now().UnixMilli()
		j.updatedAt = j.endedAt
		cancel()
		switch {
		case err == nil:
			j.status = StatusDone
			j.message = ""
		case errors.Is(err, context.Canceled):
			j.status = StatusCanceled
		default:
			j.status = StatusFailed
			j.errText = err.Error()
		}
		if m.byKind[kind] == j {
			delete(m.byKind, kind)
		}
	}()

	return j, nil
}

// Cancel 请求取消。已结束的作业返回 false。
func (m *Manager) Cancel(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[id]
	if !ok || j.status != StatusRunning {
		return false
	}
	j.cancel()
	return true
}

// CancelAll 取消所有在跑的作业。退出前调用，让扫描在文件边界停下来，
// 而不是被进程直接杀掉 —— 正在复制的那个文件会因此留下半截。
func (m *Manager) CancelAll() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	count := 0
	for _, j := range m.jobs {
		if j.status == StatusRunning {
			j.cancel()
			count++
		}
	}
	return count
}

// Running 返回还在跑的作业数。退出时用它判断「作业是否真的停了」。
func (m *Manager) Running() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	count := 0
	for _, j := range m.jobs {
		if j.status == StatusRunning {
			count++
		}
	}
	return count
}

// Get 取单个作业快照。
func (m *Manager) Get(id string) (Snapshot, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[id]
	if !ok {
		return Snapshot{}, false
	}
	return j.snapshotLocked(), true
}

// List 返回最近的作业，新的在前。
func (m *Manager) List() []Snapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	list := make([]Snapshot, 0, len(m.jobs))
	for _, j := range m.jobs {
		list = append(list, j.snapshotLocked())
	}
	// 按开始时间倒序，稳定可用（作业数量很少）。
	for i := 1; i < len(list); i++ {
		for k := i; k > 0 && list[k].StartedAt > list[k-1].StartedAt; k-- {
			list[k], list[k-1] = list[k-1], list[k]
		}
	}
	return list
}

func (j *Job) snapshotLocked() Snapshot {
	shot := Snapshot{
		ID: j.id, Kind: j.kind, Status: j.status,
		Total: j.total, Done: j.done, Skipped: j.skipped, Failed: j.failed,
		Message: j.message, Error: j.errText,
		StartedAt: j.startedAt, UpdatedAt: j.updatedAt, EndedAt: j.endedAt,
	}
	if len(j.failures) > 0 {
		shot.Failures = append([]Failure(nil), j.failures...)
	}
	if j.result != nil && j.status != StatusRunning {
		if raw, err := json.Marshal(j.result); err == nil {
			shot.Result = raw
		}
	}
	return shot
}

// pruneLocked 只保留最近 keep 个已结束的作业，正在跑的永不清。
func (m *Manager) pruneLocked() {
	if len(m.jobs) <= m.keep+1 {
		return
	}
	finished := make([]*Job, 0, len(m.jobs))
	for _, j := range m.jobs {
		if j.status != StatusRunning {
			finished = append(finished, j)
		}
	}
	if len(finished) <= m.keep {
		return
	}
	// 结束早的先删。
	for i := 0; i < len(finished)-1; i++ {
		for k := i + 1; k < len(finished); k++ {
			if finished[k].endedAt < finished[i].endedAt {
				finished[i], finished[k] = finished[k], finished[i]
			}
		}
	}
	for _, j := range finished[:len(finished)-m.keep] {
		delete(m.jobs, j.id)
	}
}
