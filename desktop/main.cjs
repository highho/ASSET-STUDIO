const { app, BrowserWindow, dialog, Menu, shell } = require('electron')
const path = require('node:path')
const fs = require('node:fs')
const http = require('node:http')
const net = require('node:net')
const { spawn } = require('node:child_process')

const APP_TITLE = 'ASSET STUDIO'
const APP_ICON = path.join(__dirname, 'build', 'icon.png')
const DEFAULT_PORT = Number(process.env.ASSET_STUDIO_PORT || 17890)
const DEV_SERVER_URL = process.env.ASSET_STUDIO_DEV_SERVER || 'http://127.0.0.1:8080'

let mainWindow = null
let splashWindow = null
let serverProcess = null
let serverLog = null
let quitting = false

if (!app.requestSingleInstanceLock()) {
  app.quit()
} else {
  app.on('second-instance', () => {
    if (mainWindow) {
      if (mainWindow.isMinimized()) mainWindow.restore()
      mainWindow.focus()
    }
  })

  app.whenReady().then(bootstrap).catch((error) => {
    showFatal(error)
  })

  app.on('window-all-closed', () => {
    app.quit()
  })

  app.on('before-quit', () => {
    quitting = true
    stopServer()
  })
}

async function bootstrap() {
  Menu.setApplicationMenu(null)

  const devUrl = process.env.ASSET_STUDIO_DEV_URL
  if (devUrl) {
    await openApp(devUrl)
    return
  }

  if (!app.isPackaged && (await isHealthy(DEV_SERVER_URL)) && process.env.ASSET_STUDIO_REUSE_DEV_SERVER !== '0') {
    await openApp(DEV_SERVER_URL)
    return
  }

  if (app.isPackaged) showSplash()

  const port = await findFreePort(DEFAULT_PORT)
  const serverPath = app.isPackaged
    ? path.join(process.resourcesPath, 'bin', 'asset-studio-server.exe')
    : path.join(__dirname, 'bin', 'asset-studio-server.exe')

  if (!fs.existsSync(serverPath)) {
    throw new Error(`找不到本机服务程序：${serverPath}\n先运行 scripts\\build-desktop.ps1 生成桌面版服务。`)
  }

  const dataDir = resolveDataDir()
  fs.mkdirSync(dataDir, { recursive: true })
  const webDir = app.isPackaged ? path.join(process.resourcesPath, 'web') : path.join(__dirname, '..', 'dist')
  if (!fs.existsSync(path.join(webDir, 'index.html'))) {
    throw new Error(`找不到前端构建产物：${webDir}\n先运行 npm run build。`)
  }

  const logDir = app.getPath('userData')
  fs.mkdirSync(logDir, { recursive: true })
  serverLog = fs.openSync(path.join(logDir, 'server.log'), 'a')

  serverProcess = spawn(serverPath, [
    '-addr', `127.0.0.1:${port}`,
    '-data', dataDir,
    '-web', webDir,
  ], {
    cwd: path.dirname(serverPath),
    windowsHide: true,
    stdio: ['ignore', serverLog, serverLog],
  })

  serverProcess.on('error', (error) => {
    if (!quitting) showFatal(error)
  })
  serverProcess.on('exit', (code) => {
    serverProcess = null
    if (!quitting) showFatal(new Error(`本机服务已退出（exit ${code}）。日志：${path.join(logDir, 'server.log')}`))
  })

  const localUrl = `http://127.0.0.1:${port}`
  await waitForHealth(localUrl, 30000)
  await openApp(localUrl)
  closeSplash()
}

function resolveDataDir() {
  if (process.env.ASSET_STUDIO_DATA) return path.resolve(process.env.ASSET_STUDIO_DATA)
  if (app.isPackaged) return path.join(app.getPath('userData'), 'data')
  return path.join(__dirname, '..', 'server', 'data')
}

async function openApp(url) {
  mainWindow = new BrowserWindow({
    width: 1280,
    height: 860,
    minWidth: 900,
    minHeight: 620,
    title: APP_TITLE,
    icon: APP_ICON,
    backgroundColor: '#c0c0c0',
    autoHideMenuBar: true,
    show: false,
    webPreferences: {
      contextIsolation: true,
      nodeIntegration: false,
      sandbox: true,
    },
  })

  mainWindow.once('ready-to-show', () => {
    closeSplash()
    mainWindow.show()
  })
  mainWindow.on('closed', () => {
    mainWindow = null
  })
  mainWindow.webContents.setWindowOpenHandler(({ url: target }) => {
    if (target.startsWith('http://127.0.0.1') || target.startsWith('http://localhost')) {
      return { action: 'allow' }
    }
    void shell.openExternal(target)
    return { action: 'deny' }
  })
  await mainWindow.loadURL(url)
}

function showSplash() {
  splashWindow = new BrowserWindow({
    width: 460,
    height: 250,
    frame: false,
    resizable: false,
    movable: false,
    icon: APP_ICON,
    backgroundColor: '#c0c0c0',
    show: true,
    webPreferences: { contextIsolation: true, nodeIntegration: false, sandbox: true },
  })
  splashWindow.loadFile(path.join(__dirname, 'splash.html')).catch(() => {})
}

function closeSplash() {
  if (splashWindow && !splashWindow.isDestroyed()) splashWindow.destroy()
  splashWindow = null
}

function showFatal(error) {
  closeSplash()
  const message = error instanceof Error ? error.message : String(error)
  dialog.showErrorBox(`${APP_TITLE} 启动失败`, message)
  app.quit()
}

function stopServer() {
  if (serverLog !== null) {
    try { fs.closeSync(serverLog) } catch {}
    serverLog = null
  }
  if (!serverProcess) return
  const child = serverProcess
  serverProcess = null
  try { child.kill() } catch {}
}

function isHealthy(baseUrl) {
  return new Promise((resolve) => {
    const request = http.get(`${baseUrl}/api/health`, { timeout: 1500 }, (response) => {
      response.resume()
      resolve(response.statusCode === 200)
    })
    request.on('timeout', () => {
      request.destroy()
      resolve(false)
    })
    request.on('error', () => resolve(false))
  })
}

async function waitForHealth(baseUrl, timeoutMs) {
  const deadline = Date.now() + timeoutMs
  while (Date.now() < deadline) {
    if (await isHealthy(baseUrl)) return
    await sleep(300)
  }
  throw new Error(`本机服务在 ${timeoutMs / 1000} 秒内没有就绪：${baseUrl}`)
}

function findFreePort(preferred) {
  return new Promise((resolve, reject) => {
    const tryListen = (port, fallback) => {
      const probe = net.createServer()
      probe.once('error', () => {
        if (fallback) tryListen(0, false)
        else reject(new Error('找不到可用的本地端口'))
      })
      probe.once('listening', () => {
        const actual = probe.address().port
        probe.close(() => resolve(actual))
      })
      probe.listen(port, '127.0.0.1')
    }
    tryListen(preferred, true)
  })
}

function sleep(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms))
}
