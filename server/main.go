package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"assetstudio/server/internal/api"
	"assetstudio/server/internal/store"
)

// shutdownGrace 是收到停止信号后等请求自己收尾的时间。超时就不再等：
// 关掉连接会取消请求的 context，绑在里面的 bpy 子进程随之结束。
const shutdownGrace = 15 * time.Second

// jobDrainGrace 是等作业在文件边界停下的上限。扫描一批几千个文件时，
// 停在当前文件上通常只要几十毫秒。
const jobDrainGrace = 5 * time.Second

func main() {
	addr := flag.String("addr", "127.0.0.1:8080", "监听地址；默认只绑本机。要局域网访问请显式写网卡 IP，例如 192.168.1.5:8080")
	dataDir := flag.String("data", "./data", "数据目录：SQLite 数据库与模型文件都放在这里")
	webDir := flag.String("web", "../dist", "前端构建产物目录（dist）")
	backfill := flag.Bool("backfill", false, "只补齐缺失的服务端指标与 sha256，然后退出")
	flag.Parse()

	absData, err := filepath.Abs(*dataDir)
	if err != nil {
		log.Fatalf("解析数据目录失败: %v", err)
	}
	st, err := store.Open(filepath.Join(absData, "assetstudio.db"), absData)
	if err != nil {
		log.Fatalf("初始化数据库失败: %v", err)
	}
	defer st.Close()

	// 先把上次没删干净的资产收尾，再决定要不要只跑补齐。
	if report, recErr := st.Reconcile(context.Background()); recErr != nil {
		log.Printf("启动对账失败: %v", recErr)
	} else if report.Tombstones > 0 || report.TrashCleaned > 0 {
		log.Printf("启动对账: 收尾删除 %d 条（失败 %d）、清理 trash 目录 %d 个",
			report.Purged, report.Failed, report.TrashCleaned)
	}
	if *backfill {
		runBackfill(st)
		return
	}

	apiServer := api.New(st)
	handler := apiServer.Routes()

	absWeb, err := filepath.Abs(*webDir)
	if err != nil {
		log.Fatalf("解析前端目录失败: %v", err)
	}
	indexPath := filepath.Join(absWeb, "index.html")
	if _, err := os.Stat(indexPath); err == nil {
		handler.Handle("/", spaHandler(absWeb, indexPath))
		log.Printf("已托管前端产物: %s", absWeb)
	} else {
		log.Printf("未找到前端产物 %s，仅提供 API（开发时请用 npm run dev）", indexPath)
	}

	// Ctrl+C / 关闭窗口 / 被服务管理器停止，都走同一条收尾路径：
	// 先让作业在文件边界停、关监听、等在跑的请求结束，再让进程退出。
	runCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	server := &http.Server{
		Addr:              *addr,
		Handler:           logRequests(api.WithHostGuard(api.WithCORS(handler), *addr)),
		ReadHeaderTimeout: 10 * time.Second,
		// 请求的 ctx 挂在 runCtx 上：停止信号一到，在跑的 /api/sand3d/run 立刻收尾，
		// 它拉起的 bpy 子进程（exec.CommandContext）也跟着结束，不留孤儿。
		BaseContext: func(net.Listener) context.Context { return runCtx },
	}

	// 只在 addr 省略主机名（如 ":8080"）时补上 localhost，否则按实际监听地址显示，
	// 避免 "-addr 127.0.0.1:8080" 被拼成 "http://localhost127.0.0.1:8080"。
	display := *addr
	if strings.HasPrefix(display, ":") {
		display = "localhost" + display
	}
	log.Printf("ASSET STUDIO 后端已启动: http://%s", display)
	log.Printf("数据目录: %s", absData)
	if !api.LoopbackOnly(*addr) {
		log.Printf("警告: 监听 %s 不只本机可访问。本服务没有账号体系，且 /api/sand3d/run 会执行任意 Python 代码，"+
			"同网段的任何人都能用它，请确认这是有意为之", *addr)
	}

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- server.ListenAndServe()
	}()

	select {
	case err := <-serveErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("服务启动失败: %v", err)
		}
	case <-runCtx.Done():
		log.Printf("收到停止信号，正在退出（最多等 %s）", shutdownGrace)
		started := time.Now()
		if canceled := apiServer.Shutdown(); canceled > 0 {
			log.Printf("已请求 %d 个在跑的作业停下", canceled)
			// 作业在自己的 goroutine 里跑，Shutdown 不管它们；这里等它们收尾，
			// 否则进程一退，正在写入的那个文件就成了半截。
			waitForJobs(apiServer, jobDrainGrace)
			log.Printf("作业收尾用了 %s", time.Since(started).Round(time.Millisecond))
		}
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			// 等不到就硬关：Close 会断开连接，请求 context 取消，子进程跟着结束。
			log.Printf("等待请求收尾超时（%v），改为立即关闭连接", err)
			_ = server.Close()
		}
		log.Printf("已停止")
	}
}

// waitForJobs 等到没有作业在跑，最多等 limit。等不到就放弃：
// 半截文件的代价比多等几秒小，但也不能让关闭窗口卡住不动。
func waitForJobs(apiServer *api.API, limit time.Duration) {
	deadline := time.Now().Add(limit)
	for apiServer.RunningJobs() > 0 {
		if time.Now().After(deadline) {
			log.Printf("还有 %d 个作业没停下，直接退出", apiServer.RunningJobs())
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}


func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		requestID := newRequestID()
		w.Header().Set("X-Request-Id", requestID)

		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(recorder, r)

		// 只记 /api/，静态资源与 SPA 回落不刷日志。
		if strings.HasPrefix(r.URL.Path, "/api/") {
			log.Printf("%s %s %d %dms %dB rid=%s",
				r.Method, r.URL.Path, recorder.status,
				time.Since(start).Round(time.Millisecond).Milliseconds(), recorder.bytes, requestID)
		}
	})
}

// newRequestID 给每个请求一个短 id：出问题时用户报的那串编号能和日志对上。
func newRequestID() string {
	var buf [6]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "-"
	}
	return hex.EncodeToString(buf[:])
}

// statusRecorder 记住 handler 实际写出去的状态码与字节数 ——
// 不包装 ResponseWriter 的话，日志里只能看到「请求进来了」，看不出是 200 还是 500。
type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(p []byte) (int, error) {
	n, err := r.ResponseWriter.Write(p)
	r.bytes += n
	return n, err
}

// spaHandler 平时直接给静态文件，非文件路径回落到 index.html，保证前端路由可用。
// 带扩展名的路径不回落：/assets/xxx.js 缺失就该 404，否则浏览器拿到 index.html
// 去当 JS 解析，报错信息会离谱到查不出来。
func spaHandler(dir, indexPath string) http.Handler {
	fileServer := http.FileServer(http.Dir(dir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.NotFound(w, r)
			return
		}
		candidate := filepath.Join(dir, filepath.Clean("/"+r.URL.Path))
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			fileServer.ServeHTTP(w, r)
			return
		}
		if looksLikeStaticAsset(r.URL.Path) {
			http.NotFound(w, r)
			return
		}
		http.ServeFile(w, r, indexPath)
	})
}

// looksLikeStaticAsset 判断「这个路径像不像一个文件」：最后一段带扩展名就算。
// 前端路由 /asset/a-1794… 不带扩展名，照旧回落到 index.html。
func looksLikeStaticAsset(path string) bool {
	base := path
	if index := strings.LastIndex(base, "/"); index >= 0 {
		base = base[index+1:]
	}
	if base == "" || strings.HasPrefix(base, ".") {
		return false
	}
	dot := strings.LastIndex(base, ".")
	return dot > 0 && dot < len(base)-1
}
