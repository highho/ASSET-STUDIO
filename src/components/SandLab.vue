<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, shallowRef, watch } from 'vue'
import * as THREE from 'three'
import { EditorState, StateEffect, StateField, type Extension } from '@codemirror/state'
import {
  Decoration,
  EditorView,
  MatchDecorator,
  ViewPlugin,
  crosshairCursor,
  drawSelection,
  dropCursor,
  highlightActiveLine,
  highlightActiveLineGutter,
  highlightSpecialChars,
  keymap,
  lineNumbers,
  rectangularSelection,
  type DecorationSet,
  type ViewUpdate,
} from '@codemirror/view'
import { defaultKeymap, history, historyKeymap, indentWithTab, redo, toggleComment } from '@codemirror/commands'
import { python } from '@codemirror/lang-python'
import { HighlightStyle, bracketMatching, foldGutter, foldKeymap, indentOnInput, syntaxHighlighting } from '@codemirror/language'
import {
  autocompletion,
  closeBrackets,
  closeBracketsKeymap,
  completionKeymap,
  completionStatus,
  startCompletion,
  type CompletionContext,
  type CompletionResult,
} from '@codemirror/autocomplete'
import { highlightSelectionMatches, openSearchPanel, search, searchKeymap } from '@codemirror/search'
import { tags as t } from '@lezer/highlight'
import ModelViewer from './ModelViewer.vue'
import { api, type Sand3dRunMeta, type Sand3dStatus } from '../lib/api'
import { downloadBlob, readError } from '../lib/download'
import { formatCount } from '../lib/format'
import { disposeObject, loadModelFromBlob } from '../lib/model'
import { SAND3D_HUMANOID_TEMPLATE, SAND3D_TEMPLATE, modelToSand3d } from '../lib/sand3d'
import { createAssetFromGlb, replaceAssetFromGlb } from '../stores/library'
import { pushToast } from '../stores/toast'
import type { AssetMeta } from '../types'

const props = defineProps<{
  /** 当前资产的模型；独立建模页（standalone）没有它 */
  model?: THREE.Object3D | null
  assetName?: string
  /** 当前资产 id：用来取它自己的 source.py（代码模式的「真身」） */
  assetId?: string
  /** 独立入口：没有所属资产，存盘时用自己填的名字 */
  standalone?: boolean
  /** 进页面/进代码页签时，自动按当前资产反推一段程序 */
  deriveOnMount?: boolean
}>()

/** 就地更新成功后通知宿主换模型：详情页据此重载预览与版本记录 */
const emit = defineEmits<{ updated: [asset: AssetMeta] }>()

const code = ref(SAND3D_TEMPLATE)
const fromSource = ref(false)
const status = ref<Sand3dStatus | null>(null)
const probing = ref(true)
const running = ref(false)
const saving = ref(false)
const errorMessage = ref('')
const errorDetail = ref('')
const meta = ref<Sand3dRunMeta | null>(null)
const result = shallowRef<THREE.Object3D | null>(null)
const produced = shallowRef<Blob | null>(null)
const showDetail = ref(false)
const newName = ref('代码模型')
const derivedOnce = ref(false)
const caret = ref({ line: 1, column: 1 })
const selectionCount = ref(1)
const editorHost = ref<HTMLDivElement | null>(null)
let editorView: EditorView | null = null

/** 编辑器里可补全的 sand3d 动词；只做名字提示，不猜参数签名。 */
const SAND3D_APIS = [
  { name: 'at', hint: '摆放 / 旋转一个零件' },
  { name: 'bbox', hint: '查询包围盒' },
  { name: 'box', hint: '长方体零件' },
  { name: 'copy', hint: '复制零件' },
  { name: 'cut', hint: '布尔差' },
  { name: 'cylinder', hint: '圆柱 / 管件' },
  { name: 'empty_scene', hint: '清空当前场景' },
  { name: 'export', hint: '导出 GLB' },
  { name: 'extrude', hint: '拉伸成型' },
  { name: 'fillet', hint: '倒圆角 / 倒角' },
  { name: 'fingerprint', hint: '输出模型指纹' },
  { name: 'grid', hint: '孔位阵列' },
  { name: 'group', hint: '编组多个零件' },
  { name: 'hole', hint: '打孔' },
  { name: 'join', hint: '布尔并' },
  { name: 'joint', hint: '关节' },
  { name: 'material', hint: '指定材质' },
  { name: 'mirror', hint: '镜像' },
  { name: 'offset', hint: '偏移' },
  { name: 'pocket', hint: '袋形槽' },
  { name: 'pose', hint: '摆姿态' },
  { name: 'rect', hint: '矩形轮廓' },
  { name: 'ring', hint: '圆环' },
  { name: 'seat', hint: '定位 / 座' },
  { name: 'spline', hint: '样条曲线' },
  { name: 'sphere', hint: '球体' },
  { name: 'split', hint: '拆分实体' },
  { name: 'sweep', hint: '扫掠成型' },
  { name: 'transform', hint: '变换' },
  { name: 'units', hint: '查询 / 设置单位' },
  { name: 'wire', hint: '线框' },
] as const

const sceneParts = computed(() => code.value.match(/s3d\.box\(/g)?.length ?? 0)
const contextKind = computed<'source' | 'derived' | 'standalone'>(() => {
  if (props.standalone) return 'standalone'
  return fromSource.value ? 'source' : 'derived'
})
const contextLead = computed(() => {
  if (contextKind.value === 'source') return `「${props.assetName || '当前资产'}」入库时保存的原始源码。`
  if (contextKind.value === 'derived') return `「${props.assetName || '当前资产'}」按包围盒近似反推的 ${sceneParts.value} 个盒子，不是原网格。`
  return '独立建模：从模板起步。'
})
const contextDetail = computed(() => {
  if (contextKind.value === 'source') {
    return '改参数后 Ctrl+Enter 运行；Ctrl+F 查找 / 替换（勾 Regexp 用正则），Ctrl+D 加选下一个匹配，Alt/Ctrl+点击加光标，Ctrl+Shift+[/] 折叠；点行号跳行。单位 mm，X 左右 / Y 前后 / Z 上下。'
  }
  if (contextKind.value === 'derived') {
    return '先改 s3d.box 的宽 / 进深 / 高，再改 s3d.at 位置；圆、柱、挤出要手动换 s3d.cylinder / s3d.sphere / s3d.extrude。Ctrl+F 查找 / 替换，勾 Regexp 用正则，Ctrl+Shift+[/] 折叠。单位 mm，X 左右 / Y 前后 / Z 上下。'
  }
  return '改数字 → 运行看右边预览 → 填名字存成新资产。Ctrl+Enter 运行，Tab 缩进；Ctrl+F 查找 / 替换，Ctrl+D 加选下一个匹配，Ctrl+Shift+[/] 折叠。单位 mm，Z 轴向上。'
})

const lineCount = computed(() => code.value.split('\n').length)
const errorLine = computed(() => {
  const match = `${errorMessage.value}\n${errorDetail.value}`.match(/(?:第\s*(\d+)\s*行|line\s*(\d+))/i)
  return match ? Number(match[1] || match[2]) : 0
})

/** 经典配色：只把 Python token 和 s3d.* 分开着色，其他保持黑字。 */
const classicHighlight = HighlightStyle.define([
  { tag: t.keyword, color: '#000080', fontWeight: 'bold' },
  { tag: [t.string, t.special(t.string)], color: '#800000' },
  { tag: t.comment, color: '#008000', fontStyle: 'normal' },
  { tag: [t.number, t.bool, t.null], color: '#800080' },
  { tag: [t.function(t.variableName), t.function(t.propertyName)], color: '#000080' },
])

const classicTheme = EditorView.theme(
  {
    '&': {
      height: '100%',
      backgroundColor: '#ffffff',
      color: '#000000',
    },
    '.cm-scroller': {
      overflow: 'auto',
      fontFamily: '"Courier New", Consolas, monospace',
      fontSize: '12px',
      lineHeight: '18px',
    },
    '.cm-content': {
      padding: '5px 7px',
      caretColor: '#000080',
    },
    '.cm-line': {
      padding: '0',
    },
    '.cm-gutters': {
      backgroundColor: '#e8e6e1',
      color: '#808080',
      borderRight: '1px solid #808080',
    },
    '.cm-lineNumbers .cm-gutterElement': {
      minWidth: '26px',
      padding: '0 4px 0 2px',
      fontSize: '10px',
      lineHeight: '18px',
    },
    '.cm-activeLine': { backgroundColor: '#ffffe1' },
    '.cm-activeLineGutter': { backgroundColor: '#ffffe1', color: '#000080' },
    '.cm-selectionBackground, &.cm-focused .cm-selectionBackground, .cm-content ::selection': {
      backgroundColor: 'rgba(0, 0, 128, 0.28)',
    },
    '.cm-cursor, .cm-dropCursor': { borderLeftColor: '#000080' },
    '.cm-foldGutter .cm-gutterElement': {
      color: '#000080',
      padding: '0 2px',
      fontSize: '10px',
      cursor: 'pointer',
    },
    '.cm-s3d-api': { color: '#0000ff', fontWeight: 'bold' },
    '.cm-error-line': {
      backgroundColor: '#f4d6d6',
      boxShadow: 'inset 2px 0 0 #800000',
    },
    '.cm-searchMatch': { backgroundColor: '#ffff80', outline: '1px solid #808080' },
    '.cm-searchMatch.cm-searchMatch-selected': { backgroundColor: '#ffb060' },
    '.cm-tooltip': {
      border: '1px solid #808080',
      backgroundColor: '#ffffff',
      color: '#000000',
    },
    '.cm-tooltip-autocomplete': {
      border: '2px solid',
      borderColor: '#ffffff #808080 #808080 #ffffff',
      backgroundColor: '#ffffff',
      boxShadow: '2px 2px 0 #000000',
      fontFamily: 'Tahoma, "MS Sans Serif", "宋体", sans-serif',
      maxWidth: '300px',
    },
    '.cm-tooltip-autocomplete > ul > li': {
      padding: '1px 5px',
      display: 'flex',
      alignItems: 'center',
      gap: '4px',
      overflow: 'hidden',
      fontFamily: '"Courier New", Consolas, monospace',
      fontSize: '10px',
      lineHeight: '18px',
    },
    '.cm-completionDetail': {
      marginLeft: 'auto',
      overflow: 'hidden',
      color: '#404040',
      fontSize: '10px',
      textOverflow: 'ellipsis',
      whiteSpace: 'nowrap',
    },
    '.cm-tooltip.cm-completionInfo': {
      display: 'none',
    },
    '.cm-tooltip-autocomplete > ul > li[aria-selected]': {
      backgroundColor: '#000080',
      color: '#ffffff',
    },
    '.cm-panel.cm-search': {
      padding: '3px 4px',
      backgroundColor: '#d4d0c8',
      color: '#000000',
      fontFamily: 'Tahoma, "MS Sans Serif", "宋体", sans-serif',
      fontSize: '10px',
      borderBottom: '2px solid',
      borderColor: '#808080 #ffffff #ffffff #808080',
    },
    '.cm-panel.cm-search input.cm-textfield': {
      width: '210px',
      padding: '1px 3px',
      border: '2px solid',
      borderColor: '#808080 #ffffff #ffffff #808080',
      backgroundColor: '#ffffff',
      color: '#000000',
      fontFamily: '"Courier New", Consolas, monospace',
      fontSize: '10px',
    },
    '.cm-panel.cm-search button.cm-button': {
      padding: '1px 5px',
      border: '2px solid',
      borderColor: '#ffffff #808080 #808080 #ffffff',
      backgroundColor: '#d4d0c8',
      color: '#000000',
      fontFamily: 'Tahoma, "MS Sans Serif", "宋体", sans-serif',
      fontSize: '10px',
    },
    '.cm-panel.cm-search label': { fontSize: '10px', margin: '0 4px 0 2px' },
    '.cm-panel.cm-search input[type=checkbox]': { accentColor: '#000080' },
  },
  { dark: false },
)

/** 把 s3d.at / s3d.box 这类动词标成蓝色；纯装饰，不参与语法解析。 */
const apiMatcher = new MatchDecorator({
  regexp: /\bs3d\.\w+/g,
  decoration: Decoration.mark({ class: 'cm-s3d-api' }),
})
const apiHighlight = ViewPlugin.fromClass(
  class {
    decorations: DecorationSet

    constructor(view: EditorView) {
      this.decorations = apiMatcher.createDeco(view)
    }

    update(update: ViewUpdate): void {
      this.decorations = apiMatcher.updateDeco(update, this.decorations)
    }
  },
  {
    decorations: (value) => value.decorations,
  },
)

/** 运行失败时给指定行铺一层淡红底；文档一改就自动清掉。 */
const setErrorLine = StateEffect.define<number>()
const errorLineField = StateField.define<DecorationSet>({
  create: () => Decoration.none,
  update(value, tr) {
    if (tr.docChanged) return Decoration.none
    let next = value.map(tr.changes)
    for (const effect of tr.effects) {
      if (!effect.is(setErrorLine)) continue
      if (!effect.value) return Decoration.none
      const line = tr.state.doc.line(Math.min(effect.value, tr.state.doc.lines))
      next = Decoration.set([Decoration.line({ class: 'cm-error-line' }).range(line.from)])
    }
    return next
  },
  provide: (field) => EditorView.decorations.from(field),
})

/** `s3d.` 后面的 API 候选；注释行不弹，避免写注释时被挡。 */
function sand3dSource(context: CompletionContext): CompletionResult | null {
  const line = context.state.doc.lineAt(context.pos)
  const before = line.text.slice(0, context.pos - line.from)
  if (before.includes('#')) return null
  const match = before.match(/s3d\.([A-Za-z0-9_]*)$/)
  if (!match) return null
  const prefix = match[1].toLowerCase()
  const options = SAND3D_APIS.filter((item) => item.name.startsWith(prefix))
    .slice(0, 8)
    .map((item) => ({
      label: item.name,
      detail: item.hint,
      info: `s3d.${item.name} — ${item.hint}`,
      type: 'function',
    }))
  return {
    from: context.pos - match[1].length,
    options,
    validFor: /^[A-Za-z0-9_]*$/,
  }
}

function syncCaretFromState(state: EditorState): void {
  const head = state.selection.main.head
  const line = state.doc.lineAt(head)
  caret.value = { line: line.number, column: head - line.from + 1 }
  selectionCount.value = state.selection.ranges.length
}

/** 输入到 `s3d.` 结尾时主动打开补全；CodeMirror 默认只在单词字符后触发。 */
function maybeStartCompletion(update: ViewUpdate): void {
  if (!editorView || completionStatus(update.state) === 'active') return
  // 只在用户敲入一个字符时检查；接受补全会一次写入整段名字，不能再次弹层。
  if (update.state.doc.length !== update.startState.doc.length + 1) return
  const head = update.state.selection.main.head
  const line = update.state.doc.lineAt(head)
  const before = line.text.slice(0, head - line.from)
  if (before.includes('#') || !/s3d\.[A-Za-z0-9_]*$/.test(before)) return
  queueMicrotask(() => {
    if (editorView) {
      startCompletion(editorView)
      nudgeTooltip()
    }
  })
}

/** 工具浮层依赖 requestAnimationFrame；页面在后台时可能停在 -10000px，主动补一次定位。 */
function nudgeTooltip(): void {
  window.setTimeout(() => {
    positionTooltipFallback()
  }, 0)
  window.setTimeout(() => {
    positionTooltipFallback()
  }, 120)
}

/** 页面在后台时 view.inView 为 false，CM 不会写浮层坐标；用光标坐标补位。 */
function positionTooltipFallback(): void {
  if (!editorView || !editorHost.value) return
  const tip = editorHost.value.querySelector('.cm-tooltip-autocomplete') as HTMLElement | null
  if (!tip) return
  const hostRect = editorHost.value.getBoundingClientRect()
  const current = tip.getBoundingClientRect()
  const outside = tip.style.top === '-10000px' || current.top < hostRect.top || current.bottom > hostRect.bottom + 4
  if (!outside) return
  const coords = editorView.coordsAtPos(editorView.state.selection.main.head)
  if (!coords) return
  const width = tip.offsetWidth || 260
  const height = tip.offsetHeight || 120
  const left = Math.max(4, Math.min(coords.left, window.innerWidth - width - 8))
  const top = coords.bottom + height + 6 > hostRect.bottom ? Math.max(hostRect.top + 4, coords.top - height - 4) : coords.bottom + 4
  tip.style.left = `${left}px`
  tip.style.top = `${top}px`
}

function onEditorUpdate(update: ViewUpdate): void {
  if (update.docChanged) {
    const text = update.state.doc.toString()
    if (text !== code.value) code.value = text
    maybeStartCompletion(update)
  }
  syncCaretFromState(update.state)
}

/** 点行号跳行；CodeMirror 默认行号只显示不响应点击。 */
function onGutterMouseDown(event: MouseEvent): void {
  const target = event.target as HTMLElement | null
  if (!target) return
  const gutter = target.closest('.cm-lineNumbers')
  if (!gutter) return
  const element = target.closest('.cm-gutterElement')
  if (!element || !gutter.contains(element)) return
  const line = Number((element.textContent || '').trim())
  if (!Number.isInteger(line) || line < 1) return
  event.preventDefault()
  gotoLine(line)
}

function gotoLine(line: number): void {
  if (!editorView) return
  const target = Math.max(1, Math.min(line, editorView.state.doc.lines))
  const position = editorView.state.doc.line(target).from
  editorView.dispatch({
    selection: { anchor: position },
    effects: EditorView.scrollIntoView(position, { y: 'center' }),
  })
  editorView.focus()
}

function markErrorLine(line: number): void {
  if (!editorView || line < 1) return
  editorView.dispatch({ effects: setErrorLine.of(line) })
}

function clearErrorLine(): void {
  if (!editorView) return
  editorView.dispatch({ effects: setErrorLine.of(0) })
}

/** 外部载入源码 / 模板时整体换文档，顺便重置撤销栈和滚动位置。 */
function replaceEditorDoc(text: string): void {
  if (!editorView) return
  if (editorView.state.doc.toString() === text) return
  editorView.setState(EditorState.create({ doc: text, extensions }))
  syncCaretFromState(editorView.state)
}

watch(code, (value) => replaceEditorDoc(value), { flush: 'sync' })

const appKeymap = keymap.of([
  {
    key: 'Mod-Enter',
    run: () => {
      void run()
      return true
    },
  },
  { key: 'Mod-h', run: openSearchPanel },
  { key: 'Mod-/', run: toggleComment },
  { key: 'Mod-y', run: redo },
  indentWithTab,
])

const libraryKeymap = keymap.of([
  ...closeBracketsKeymap,
  ...defaultKeymap,
  ...searchKeymap,
  ...historyKeymap,
  ...foldKeymap,
  ...completionKeymap,
])

const extensions: Extension[] = [
  lineNumbers(),
  highlightActiveLineGutter(),
  highlightActiveLine(),
  highlightSpecialChars(),
  highlightSelectionMatches(),
  drawSelection(),
  dropCursor(),
  rectangularSelection(),
  crosshairCursor(),
  history(),
  indentOnInput(),
  python(),
  bracketMatching(),
  closeBrackets(),
  foldGutter(),
  autocompletion({ override: [sand3dSource], activateOnTyping: true }),
  search({ top: true }),
  syntaxHighlighting(classicHighlight),
  classicTheme,
  apiHighlight,
  errorLineField,
  EditorState.phrases.of({
    Find: '查找',
    Replace: '替换',
    next: '下一个',
    previous: '上一个',
    all: '全部',
    'match case': '区分大小写',
    regexp: '正则',
    'by word': '整词',
    replace: '替换',
    'replace all': '全部替换',
    close: '关闭',
    'Go to line': '跳到行',
    go: '跳转',
  }),
  EditorState.tabSize.of(4),
  EditorState.allowMultipleSelections.of(true),
  EditorView.clickAddsSelectionRange.of((event) => event.altKey || event.ctrlKey || event.metaKey),
  EditorView.contentAttributes.of({
    spellcheck: 'false',
    autocapitalize: 'off',
    'aria-label': 'sand3d 程序编辑器',
  }),
  EditorView.updateListener.of(onEditorUpdate),
  appKeymap,
  libraryKeymap,
]

/** 独立建模页的两个起步模板：都是能真跑的程序，不是示意代码 */
const templates = [
  { key: 'bracket', label: '板件模板', name: '支架模板', code: SAND3D_TEMPLATE },
  { key: 'humanoid', label: '人偶模板', name: '参数化人偶', code: SAND3D_HUMANOID_TEMPLATE },
] as const

function applyTemplate(template: (typeof templates)[number]): void {
  code.value = template.code
  newName.value = template.name
  fromSource.value = false
  errorMessage.value = ''
  errorDetail.value = ''
  pushToast(`已载入${template.label}，改数字再点运行`, 'info')
}

function disposeResult(): void {
  const current = result.value
  result.value = null
  if (current) window.setTimeout(() => disposeObject(current), 0)
}

async function probe(): Promise<void> {
  probing.value = true
  try {
    status.value = await api.sand3dStatus()
  } catch (error) {
    status.value = { available: false, error: readError(error) }
  } finally {
    probing.value = false
  }
}

async function run(): Promise<void> {
  if (running.value) return
  running.value = true
  errorMessage.value = ''
  errorDetail.value = ''
  showDetail.value = false
  clearErrorLine()
  try {
    const { blob, meta: info } = await api.sand3dRun(code.value)
    const object = await loadModelFromBlob(blob, 'glb')
    disposeResult()
    result.value = object
    produced.value = blob
    meta.value = info
    pushToast(
      `已生成 ${formatCount(info.faces)} 个面 · ${(info.elapsedMs / 1000).toFixed(1)} 秒`,
      'success',
    )
  } catch (error) {
    const failure = error as Error & { detail?: string }
    errorMessage.value = failure.message || readError(error)
    errorDetail.value = failure.detail ?? ''
    void nextTick(() => {
      if (errorLine.value > 0 && errorLine.value <= lineCount.value) {
        markErrorLine(errorLine.value)
        gotoLine(errorLine.value)
      }
    })
  } finally {
    running.value = false
  }
}

/** 代码模式的「真身」：资产存库时写下的 source.py。取不到才退回按模型反推。 */
async function loadSource(): Promise<boolean> {
  if (!props.assetId) return false
  const text = await api.sidecarText(props.assetId, 'source.py')
  if (!text || !text.trim()) return false
  code.value = text
  fromSource.value = true
  derivedOnce.value = true
  return true
}

/** 由模型推代码：当前资产 → 一段可改的 sand3d 程序 */
function reverse(): void {
  if (!props.model) {
    pushToast('当前没有模型可反推', 'error')
    return
  }
  const { code: generated, parts, note, warning } = modelToSand3d(props.model, props.assetName || '')
  code.value = generated
  fromSource.value = false
  errorMessage.value = ''
  errorDetail.value = ''
  derivedOnce.value = true
  if (warning) {
    pushToast(warning, 'error')
    return
  }
  pushToast(`${note}（${parts} 个零件），改完数字再点运行`, 'info')
}

function saveAsset(): void {
  if (!produced.value) {
    pushToast('先运行一次，拿到模型再存成资产', 'error')
    return
  }
  const fallback = props.standalone ? newName.value.trim() : `${props.assetName} · sand3d`
  if (!fallback) {
    pushToast('先给新资产起个名字', 'error')
    return
  }
  saving.value = true
  createAssetFromGlb(produced.value, fallback, code.value)
    .catch((error) => pushToast(`入库失败：${readError(error)}`, 'error'))
    .finally(() => {
      saving.value = false
    })
}

/** 就地更新：把这次跑出来的 GLB 换成当前资产的当前版，旧版留给版本记录。 */
async function updateCurrentAsset(): Promise<void> {
  if (!produced.value || !props.assetId) {
    pushToast('先运行一次，拿到模型再更新', 'error')
    return
  }
  saving.value = true
  try {
    const updated = await replaceAssetFromGlb(props.assetId, produced.value, code.value)
    emit('updated', updated)
  } catch (error) {
    pushToast(`更新失败：${readError(error)}`, 'error')
  } finally {
    saving.value = false
  }
}

async function copyCode(): Promise<void> {
  try {
    await navigator.clipboard.writeText(code.value)
    pushToast('代码已复制', 'success')
  } catch {
    pushToast('这个浏览器不允许写剪贴板', 'error')
  }
}

function downloadCode(): void {
  const blob = new Blob([code.value], { type: 'text/x-python;charset=utf-8' })
  const name = (props.assetName || 'sand3d').replace(/[\\/:*?"<>|\s]+/g, '_')
  downloadBlob(blob, `${name}.py`)
}

onMounted(() => {
  if (editorHost.value) {
    editorView = new EditorView({
      state: EditorState.create({ doc: code.value, extensions }),
      parent: editorHost.value,
    })
    syncCaretFromState(editorView.state)
    editorHost.value.addEventListener('mousedown', onGutterMouseDown)
    editorView.focus()
  }
  void probe()
  if (props.deriveOnMount) {
    void (async () => {
      const loaded = await loadSource()
      if (!loaded && props.model) reverse()
    })()
  }
})

// 详情页的模型是异步加载的：加载完了再补一次自动反推，用户看到的直接是这个资产的程序。
// 已经有 source.py 的资产不反推——那会把「原本的程序」换成盒子近似。
watch(
  () => props.model,
  (model) => {
    if (model && props.deriveOnMount && !derivedOnce.value && !fromSource.value) reverse()
  },
)

onBeforeUnmount(() => {
  if (editorHost.value) editorHost.value.removeEventListener('mousedown', onGutterMouseDown)
  editorView?.destroy()
  editorView = null
  disposeResult()
})
</script>


<template>
  <div class="lab">
    <div class="pane">
      <header class="bar">
        <span class="badge mono">s3d · bpy</span>
        <span class="hint-line">
          {{ standalone ? '写程序造一个新模型，跑通再存成资产' : fromSource ? `「${assetName}」存库时写下的源码` : `这段程序描述「${assetName}」怎么拼出来` }}
        </span>
        <div class="actions">
          <button class="btn btn-primary" type="button" :disabled="running" title="Ctrl+Enter 运行，结果直接在下方预览" @click="run">
            {{ running ? '运行中…' : '运行  ⌃↵' }}
          </button>
        </div>
      </header>

      <header class="bar is-secondary">
        <div v-if="standalone" class="save-name">
          <button
            v-for="template in templates"
            :key="template.key"
            class="btn btn-ghost btn-sm"
            type="button"
            @click="applyTemplate(template)"
          >
            {{ template.label }}
          </button>
          <input v-model="newName" class="field field-sm" type="text" maxlength="64" placeholder="新资产名" />
        </div>
        <div class="actions">
          <button class="btn" type="button" :disabled="!model" title="按当前模型的世界包围盒反推一段可编辑程序，会覆盖编辑器内容" @click="reverse">由模型推代码</button>
          <button
            v-if="!standalone && assetId"
            class="btn btn-primary"
            type="button"
            :disabled="!produced || saving"
            title="先运行一次；更新后旧版会保留在版本记录里"
            @click="updateCurrentAsset"
          >
            {{ saving ? '更新中…' : '更新当前资产' }}
          </button>
          <button class="btn" type="button" :disabled="!produced || saving" title="把本次运行结果和源码一起存成一个新资产" @click="saveAsset">
            {{ saving ? '入库中…' : '存为新资产' }}
          </button>
          <button class="btn btn-ghost" type="button" title="复制整段程序" @click="copyCode">复制</button>
          <button class="btn btn-ghost" type="button" title="下载当前源码为 .py" @click="downloadCode">下载 .py</button>
        </div>
      </header>

      <div class="context-tip">
        <span class="tip-badge mono">代码提示</span>
        <p><strong>{{ contextLead }}</strong>{{ contextDetail }}</p>
      </div>

      <div class="editor-shell">
        <div ref="editorHost" class="cm-host" @mousedown="onGutterMouseDown"></div>
      </div>

      <footer class="status">
        <div v-if="errorMessage" class="status error">
          <strong>{{ errorMessage }}</strong>
          <button v-if="errorDetail" class="link" type="button" @click="showDetail = !showDetail">
            {{ showDetail ? '收起 traceback' : '看 traceback' }}
          </button>
          <pre v-if="showDetail" class="trace mono">{{ errorDetail }}</pre>
        </div>
        <div v-else-if="probing" class="status muted">正在确认本机 sand3d 环境…</div>
        <div v-else-if="status && !status.available" class="status warn">
          <strong>本机还不能真跑</strong>
          <span>{{ status.error }}</span>
        </div>
        <div v-else class="status ok">
          <span class="dot"></span>
          <span class="mono">
            sand3d {{ status?.version }} · {{ status?.runtime === 'blender' ? 'Blender 自带 bpy' : 'pip bpy' }}
          </span>
          <span class="status-right">
            <span class="mono caret">
              行 {{ caret.line }} · 列 {{ caret.column }}<template v-if="selectionCount > 1"> · {{ selectionCount }} 处光标</template>
            </span>
            <span v-if="meta" class="mono result">
              {{ meta.objects.length }} 个零件 · {{ formatCount(meta.faces) }} 面 ·
              {{ formatCount(meta.vertices) }} 顶点 · {{ meta.units }} · {{ (meta.elapsedMs / 1000).toFixed(1) }}s
            </span>
          </span>
        </div>
      </footer>
    </div>

    <div class="preview">
      <template v-if="result">
        <ModelViewer :model="result" :asset-name="assetName" mode="realistic" />
        <div class="preview-tag mono">
          {{ meta?.fingerprint ? `fingerprint ${meta.fingerprint}` : 'blender 导出' }}
        </div>
      </template>
      <div v-else class="empty">
        <p class="mono">NO PROGRAM RUN YET</p>
        <h3>写几行 Python，点运行</h3>
        <p>
          这段代码会交给本机的 bpy 真跑，导出的 GLB 直接在这里预览 ——
          和你在 Blender 里跑出来的模型是一个东西。
        </p>
        <p v-if="!standalone && model" class="derive-hint">
          左边已经是「{{ assetName }}」的程序（由模型反推），改数字再运行就是它的变体。
        </p>
      </div>
    </div>
  </div>
</template>

<style scoped>
.lab {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(0, 1.1fr);
  height: 100%;
  background: var(--surface);
}

.pane {
  display: flex;
  flex-direction: column;
  min-width: 0;
  border-right: 2px solid #808080;
  background: var(--surface);
}

.bar {
  display: flex;
  align-items: center;
  gap: 4px;
  padding: 3px 4px;
  border-bottom: 2px solid;
  border-color: #ffffff #808080 #808080 #ffffff;
  background: var(--surface);
}

.bar.is-secondary {
  border-top: 1px solid #ffffff;
  background: var(--surface);
}

.badge {
  padding: 1px 4px;
  border: 1px solid #808080;
  border-radius: 0;
  background: #c0c0c0;
  color: #000080;
  font-size: 9px;
  letter-spacing: 0;
}

.hint-line {
  color: #333333;
  font-size: 10px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.save-name {
  display: flex;
  align-items: center;
  gap: 3px;
  min-width: 0;
}

.save-name .btn {
  padding: 1px 6px;
  font-size: 10px;
}

.field-sm {
  width: 130px;
  padding: 1px 3px;
  font-size: 11px;
}

.derive-hint {
  margin-top: 3px;
  color: #000080;
  font-size: 10.5px;
  line-height: 1.5;
}

.actions {
  display: flex;
  gap: 3px;
  margin-left: auto;
}

.actions .btn {
  padding: 1px 6px;
  font-size: 10px;
}

.context-tip {
  display: flex;
  align-items: flex-start;
  gap: 4px;
  flex: none;
  margin: 3px 3px 0;
  padding: 3px 5px;
  border: 1px solid #000000;
  background: #ffffe1;
  color: #000000;
  font-size: 10px;
  line-height: 1.4;
}

.tip-badge {
  flex: none;
  padding: 0 3px;
  border: 1px solid #808080;
  background: #c0c0c0;
  color: #000080;
  font-size: 9px;
}

.context-tip p {
  margin: 0;
}

.context-tip strong {
  margin-right: 3px;
  color: #000080;
}

.editor-shell {
  display: flex;
  flex: 1;
  min-height: 0;
  margin: 3px;
  border: 2px solid;
  border-color: var(--bevel-sunken);
  background: #ffffff;
  overflow: hidden;
}

.cm-host {
  flex: 1;
  min-width: 0;
  min-height: 0;
  height: 100%;
  overflow: hidden;
  background: #ffffff;
}

.status {
  padding: 2px 4px;
  border-top: 2px solid;
  border-color: #808080 #ffffff #ffffff #808080;
  background: var(--surface);
  font-size: 10px;
  min-height: 20px;
}

.status.muted {
  color: #333333;
}

.status.ok {
  display: flex;
  align-items: center;
  gap: 4px;
  color: #000000;
}

.status .dot {
  width: 6px;
  height: 6px;
  border-radius: 0;
  background: #008000;
  flex: none;
}

.status .result {
  margin-left: 0;
  color: #333333;
}

.status-right {
  display: flex;
  align-items: center;
  gap: 10px;
  min-width: 0;
  margin-left: auto;
}

.status .caret {
  flex: none;
  color: #333333;
  white-space: nowrap;
}

.status.warn {
  display: flex;
  flex-direction: column;
  gap: 2px;
  color: #805000;
}

.status.error {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: 4px;
  color: #800000;
}

.status.error strong {
  font-weight: 700;
}

.link {
  border: 0;
  background: none;
  color: #000080;
  font-family: inherit;
  font-size: 10px;
  text-decoration: underline;
  cursor: pointer;
}

.trace {
  width: 100%;
  max-height: 160px;
  margin: 3px 0 0;
  overflow: auto;
  padding: 4px;
  border: 2px solid;
  border-color: var(--bevel-sunken);
  border-radius: 0;
  background: #ffffff;
  color: #000000;
  font-size: 10px;
  line-height: 1.4;
  white-space: pre-wrap;
}

.preview {
  position: relative;
  min-width: 0;
  min-height: 0;
  background: var(--surface);
}

.preview-tag {
  position: absolute;
  right: 8px;
  top: 34px;
  padding: 1px 4px;
  border: 1px solid #808080;
  border-radius: 0;
  background: #d4d0c8;
  color: #000000;
  font-size: 9px;
  pointer-events: none;
}

.empty {
  position: absolute;
  left: 8px;
  right: 8px;
  top: 50%;
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 4px;
  width: auto;
  max-width: 420px;
  height: auto;
  margin: 0 auto;
  padding: 10px;
  border: 2px solid;
  border-color: var(--bevel-sunken);
  background: #ffffff;
  transform: translateY(-50%);
}

.empty .mono {
  color: #000080;
  font-size: 10px;
  letter-spacing: 0;
}

.empty h3 {
  color: #000000;
  font-size: 13px;
}

.empty p {
  color: #333333;
  font-size: 11px;
  line-height: 1.5;
}

@media (max-width: 1180px) {
  .lab {
    grid-template-columns: minmax(0, 1fr);
    grid-template-rows: minmax(0, 1fr) minmax(0, 0.85fr);
  }

  .pane {
    border-right: none;
    border-bottom: 2px solid #808080;
  }
}
</style>
