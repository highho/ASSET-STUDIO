package api

import "net/http"

// listJobs 列出最近的作业，前端在别处也能看到「刚才那次扫描跑到哪了」。
func (a *API) listJobs(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, a.jobs.List())
}

// getJob 取单个作业进度。扫描的收尾结果也在这里返回（result 字段）。
func (a *API) getJob(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	shot, ok := a.jobs.Get(id)
	if !ok {
		writeError(w, http.StatusNotFound, "作业不存在，可能已经结束并被清理")
		return
	}
	writeJSON(w, http.StatusOK, shot)
}

// cancelJob 取消一个还在跑的作业；已结束的返回 409。
func (a *API) cancelJob(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !a.jobs.Cancel(id) {
		writeError(w, http.StatusConflict, "作业已结束，无需取消")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "canceling"})
}
