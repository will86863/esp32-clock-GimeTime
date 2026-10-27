import React from 'react'
import {
  makeStyles,
  tokens,
  Text,
  Button,
  Input,
  Field,
  Dialog,
  DialogSurface,
  DialogTitle,
  DialogBody,
  DialogContent,
  DialogActions,
  Table,
  TableHeader,
  TableRow,
  TableHeaderCell,
  TableBody,
  TableCell,
  TableCellLayout,
  Badge,
  Spinner,
  Divider,
  Tooltip,
} from '@fluentui/react-components'
import {
  Add24Regular,
  FolderOpen24Regular,
  Delete24Regular,
  Edit24Regular,
  Checkmark24Regular,
  Open24Regular,
} from '@fluentui/react-icons'
import {
  App as api,
  Config,
  Game,
  LiveStatus,
  ResolveResult,
  ExeCandidate,
  registerFileDrop,
} from '../api'
import { checkDisplay, DISPLAY_MAX } from '../util'

const useStyles = makeStyles({
  wrap: { display: 'flex', flexDirection: 'column', gap: '16px' },
  head: {
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'space-between',
    gap: '12px',
  },
  actions: { display: 'flex', gap: '8px', alignItems: 'center' },
  dropZone: {
    border: `2px dashed ${tokens.colorNeutralStroke1}`,
    borderRadius: tokens.borderRadiusXLarge,
    padding: '28px 20px',
    textAlign: 'center',
    display: 'flex',
    flexDirection: 'column',
    alignItems: 'center',
    gap: '10px',
    transition: 'all .15s ease',
    backgroundColor: tokens.colorNeutralBackground2,
  },
  dropZoneActive: {
    border: `2px dashed ${tokens.colorBrandStroke1}`,
    backgroundColor: tokens.colorBrandBackground2,
  },
  // 固定布局：避免长路径把列撑开、让输入框互相压住
  table: { tableLayout: 'fixed' },
  nameCell: { display: 'flex', alignItems: 'center', gap: '8px', minWidth: 0 },
  nameText: { overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' },
  path: {
    fontSize: '12px',
    color: tokens.colorNeutralForeground3,
    overflow: 'hidden',
    textOverflow: 'ellipsis',
    whiteSpace: 'nowrap',
  },
  candRow: {
    display: 'flex',
    alignItems: 'center',
    gap: '12px',
    padding: '10px 12px',
    borderRadius: tokens.borderRadiusMedium,
    border: `1px solid ${tokens.colorNeutralStroke2}`,
    cursor: 'pointer',
    marginBottom: '8px',
  },
  candRowSel: {
    border: `2px solid ${tokens.colorBrandStroke1}`,
    backgroundColor: tokens.colorBrandBackground2,
  },
  candIcon: { width: '32px', height: '32px', flexShrink: 0 },
  candMeta: { flex: 1, minWidth: 0 },
  candName: { fontWeight: 600, wordBreak: 'break-all' },
  empty: {
    padding: '48px 20px',
    textAlign: 'center',
    color: tokens.colorNeutralForeground3,
  },
})

type AddState = {
  open: boolean
  loading: boolean
  result: ResolveResult | null
  exe: string
  display: string
  steamName: string
  appid: string
  source: string
  error: string
}

const emptyAdd: AddState = {
  open: false,
  loading: false,
  result: null,
  exe: '',
  display: '',
  steamName: '',
  appid: '',
  source: '',
  error: '',
}

export default function GamesPage({
  config,
  live,
  onChanged,
  notify,
}: {
  config: Config | null
  live: LiveStatus | null
  onChanged: () => Promise<void> | void
  notify: (t: string, b: string, i?: 'success' | 'error' | 'info') => void
}) {
  const s = useStyles()
  const [add, setAdd] = React.useState<AddState>(emptyAdd)
  const [dragOver, setDragOver] = React.useState(false)
  const [busy, setBusy] = React.useState('')

  // 改名弹窗（取代原来的行内编辑）
  // 为什么改弹窗：行内用 Field 会在下方渲染校验提示文字，把行高撑大、
  // 和下一行内容重叠。弹窗有独立空间，输入框不会再互相压住。
  const [rename, setRename] = React.useState<Game | null>(null)
  const [renameVal, setRenameVal] = React.useState('')

  const games = config?.games ?? []

  // ---------- 添加：文件对话框 ----------
  const pickFile = async () => {
    setAdd({ ...emptyAdd, open: true, loading: true })
    try {
      const r = await api.pickFile()
      if (!r || (!r.error && !r.exe && !r.steam)) {
        setAdd(emptyAdd) // 用户取消
        return
      }
      applyResolve(r)
    } catch (e: any) {
      setAdd({ ...emptyAdd, open: true, error: String(e?.message ?? e) })
    }
  }

  const resolveAndOpen = React.useCallback(async (path: string) => {
    setAdd({ ...emptyAdd, open: true, loading: true })
    try {
      const r = await api.resolvePath(path)
      applyResolve(r)
    } catch (e: any) {
      setAdd({ ...emptyAdd, open: true, error: String(e?.message ?? e) })
    }
  }, [])

  const applyResolve = (r: ResolveResult) => {
    const cands = r.candidates ?? []
    let exe = r.exe || ''
    if (!exe && cands.length === 1) exe = cands[0].path
    setAdd({
      open: true,
      loading: false,
      result: r,
      exe,
      display: r.suggested || '',
      steamName: r.steam?.name || '',
      appid: r.steam?.appid || '',
      source: r.steam ? 'steam' : r.kind,
      error: r.error || '',
    })
    // 延迟确认弹窗是否真的出现在 DOM 里
    window.setTimeout(() => {
      try {
        const n = document.querySelectorAll('[role="dialog"]').length
        void window.go?.main?.App?.LogFromFrontend?.('dialog', '弹窗数=' + n)
      } catch {
        /* 忽略 */
      }
    }, 500)
  }

  // ---------- 拖放 ----------
  // 两步缺一不可：
  //   1) registerFileDrop → 前端调用 window.runtime.OnFileDrop，
  //      Wails 才会挂上 DOM 的 dragover/drop 监听（否则 Go 侧永远收不到）
  //   2) 轮询 TakeDropPath → 从 Go 侧取走真实绝对路径（浏览器拿不到绝对路径）
  React.useEffect(() => {
    const off = registerFileDrop((paths) => {
      // 注意：拖 .url 时 WebView2 会给出 ["", "", "C:\\...\\x.url"] 这种数组
      // （前面带空串，字符串本身还可能带前导空格），直接取 paths[0] 会拿到空串
      // 导致弹窗根本不出现。所以取「第一个非空且未 trim 前非空」的项。
      const p = (paths || [])
        .map((s) => (s ?? '').trim())
        .find((s) => s.length > 0)
      if (p) void resolveAndOpen(p)
      else
        try {
          void window.go?.main?.App?.LogFromFrontend?.(
            'drop',
            '无有效路径: ' + JSON.stringify(paths),
          )
        } catch {
          /* 忽略 */
        }
    })

    const onDragOver = (e: DragEvent) => {
      if (!e.dataTransfer?.types?.includes('Files')) return
      e.preventDefault()
      e.dataTransfer.dropEffect = 'copy'
      setDragOver(true)
    }
    const onDragLeave = (e: DragEvent) => {
      if (e.relatedTarget === null) setDragOver(false)
    }
    const onDrop = (e: DragEvent) => {
      // 必须阻止默认行为，否则 WebView 会尝试「打开」被拖入的文件
      if (e.dataTransfer?.types?.includes('Files')) e.preventDefault()
      setDragOver(false)
    }

    window.addEventListener('dragover', onDragOver, true)
    window.addEventListener('dragleave', onDragLeave, true)
    window.addEventListener('drop', onDrop, true)

    return () => {
      off()
      window.removeEventListener('dragover', onDragOver, true)
      window.removeEventListener('dragleave', onDragLeave, true)
      window.removeEventListener('drop', onDrop, true)
    }
  }, [resolveAndOpen])

  // ---------- 提交添加 ----------
  const submitAdd = async () => {
    if (!add.exe) {
      setAdd((a) => ({ ...a, error: '请先选择具体的主程序 exe' }))
      return
    }
    const derr = checkDisplay(add.display)
    if (derr) {
      setAdd((a) => ({ ...a, error: derr }))
      return
    }
    setBusy('add')
    try {
      await api.addGame({
        exe: add.exe,
        display: add.display.trim(),
        appid: add.appid,
        source: add.source || 'exe',
      })
      setAdd(emptyAdd)
      await onChanged()
      notify('已添加', add.display.trim(), 'success')
    } catch (e: any) {
      setAdd((a) => ({ ...a, error: String(e?.message ?? e) }))
    } finally {
      setBusy('')
    }
  }

  // ---------- 改名 ----------
  const openRename = (g: Game) => {
    setRenameVal(g.display)
    setRename(g)
    // 确认弹窗真的渲染出来（排查界面问题用）
    window.setTimeout(() => {
      try {
        const dlg = document.querySelectorAll('[role="dialog"]')
        const inputs = document.querySelectorAll('[role="dialog"] input')
        const first = inputs[0] as HTMLElement | undefined
        const rect = first?.getBoundingClientRect()
        void window.go?.main?.App?.LogFromFrontend?.(
          'rename',
          '改名弹窗已打开 dialogs=' +
            dlg.length +
            ' inputs=' +
            inputs.length +
            ' box=' +
            (rect ? Math.round(rect.width) + 'x' + Math.round(rect.height) : 'none'),
        )
      } catch {
        /* 忽略 */
      }
    }, 400)
  }

  const submitRename = async () => {
    if (!rename) return
    const err = checkDisplay(renameVal)
    if (err) {
      notify('显示名不合法', err, 'error')
      return
    }
    setBusy(rename.id)
    try {
      await api.updateGame({ ...rename, display: renameVal.trim() })
      setRename(null)
      await onChanged()
      notify('已保存', renameVal.trim(), 'success')
    } catch (e: any) {
      notify('保存失败', String(e?.message ?? e), 'error')
    } finally {
      setBusy('')
    }
  }

  const del = async (g: Game) => {
    setBusy(g.id)
    try {
      await api.deleteGame(g.id)
      await onChanged()
      notify('已移除', `${g.display}（历史统计保留）`, 'info')
    } catch (e: any) {
      notify('移除失败', String(e?.message ?? e), 'error')
    } finally {
      setBusy('')
    }
  }

  const runningId = React.useMemo(() => {
    if (!live?.running) return ''
    const hit = games.find((g) => g.display === live.name)
    return hit?.id ?? ''
  }, [live, games])

  // 同名进程计数：同一个引擎 exe 被多个游戏共用时，界面上要能看出
  // 它们是靠「完整路径」区分的，否则用户会以为是重复添加。
  const nameCount = React.useMemo(() => {
    const m = new Map<string, number>()
    for (const g of games) {
      const k = (g.process || '').toLowerCase()
      m.set(k, (m.get(k) || 0) + 1)
    }
    return m
  }, [games])

  const sameNameCount = (process: string) => nameCount.get((process || '').toLowerCase()) ?? 0

  const cands: ExeCandidate[] = add.result?.candidates ?? []
  const renameErr = rename ? checkDisplay(renameVal) : ''

  return (
    <div className={s.wrap}>
      <div className={s.head}>
        <div>
          <Text size={500} weight="semibold" as="h2">
            监控的程序
          </Text>
          <div>
            <Text size={200} style={{ color: tokens.colorNeutralForeground3 }}>
              OLED 显示「最后启动」的那个程序；没有程序运行时显示今日总时长
            </Text>
          </div>
        </div>
        <div className={s.actions}>
          <Button appearance="primary" icon={<FolderOpen24Regular />} onClick={pickFile}>
            选择程序…
          </Button>
        </div>
      </div>

      {/* 拖放区 */}
      <div className={`${s.dropZone} ${dragOver ? s.dropZoneActive : ''}`}>
        <Add24Regular />
        <Text weight="semibold">把程序或快捷方式拖到这里</Text>
        <Text size={200} style={{ color: tokens.colorNeutralForeground3 }}>
          支持 .exe、桌面快捷方式 .lnk、Steam 游戏快捷方式 .url
        </Text>
      </div>

      {/* 列表 */}
      {games.length === 0 ? (
        <div className={s.empty}>
          <Text size={400}>还没有添加任何程序</Text>
          <div style={{ marginTop: 8 }}>
            <Text size={200}>点「选择程序…」或直接把桌面快捷方式拖到上面</Text>
          </div>
        </div>
      ) : (
        <Table size="small" className={s.table}>
          <TableHeader>
            <TableRow>
              <TableHeaderCell style={{ width: '26%' }}>显示名（OLED）</TableHeaderCell>
              <TableHeaderCell style={{ width: '52%' }}>程序路径</TableHeaderCell>
              <TableHeaderCell style={{ width: '10%' }}>来源</TableHeaderCell>
              <TableHeaderCell style={{ width: '12%' }}>操作</TableHeaderCell>
            </TableRow>
          </TableHeader>
          <TableBody>
            {games.map((g) => (
              <TableRow key={g.id}>
                <TableCell>
                  <TableCellLayout>
                    <div className={s.nameCell}>
                      <Text weight="semibold" size={400} className={s.nameText}>
                        {g.display}
                      </Text>
                      {runningId === g.id ? (
                        <Badge appearance="filled" color="success" size="small">
                          运行中
                        </Badge>
                      ) : null}
                      <Tooltip content="修改显示名" relationship="label">
                        <Button
                          size="small"
                          appearance="subtle"
                          icon={<Edit24Regular />}
                          onClick={() => openRename(g)}
                        />
                      </Tooltip>
                    </div>
                  </TableCellLayout>
                </TableCell>
                <TableCell>
                  <TableCellLayout>
                    <div className={s.path}>{g.exe}</div>
                    <div style={{ display: 'flex', alignItems: 'center', gap: 6, minWidth: 0 }}>
                      <Text size={100} style={{ color: tokens.colorNeutralForeground3 }}>
                        进程 {g.process}
                      </Text>
                      {sameNameCount(g.process) > 1 ? (
                        <Tooltip
                          content="多个程序共用这个引擎进程名，靠完整路径区分，互不影响"
                          relationship="description"
                        >
                          <Badge appearance="tint" size="small" color="warning">
                            同名 ×{sameNameCount(g.process)}
                          </Badge>
                        </Tooltip>
                      ) : null}
                    </div>
                  </TableCellLayout>
                </TableCell>
                <TableCell>
                  <Badge appearance="tint" size="small">
                    {g.appid ? 'Steam' : g.source || 'exe'}
                  </Badge>
                </TableCell>
                <TableCell>
                  <div style={{ display: 'flex', gap: 4 }}>
                    <Tooltip content="打开所在文件夹" relationship="label">
                      <Button
                        size="small"
                        appearance="subtle"
                        icon={<Open24Regular />}
                        onClick={() => api.openGameFolder(g.exe)}
                      />
                    </Tooltip>
                    <Tooltip content="移除" relationship="label">
                      <Button
                        size="small"
                        appearance="subtle"
                        icon={<Delete24Regular />}
                        disabled={busy === g.id}
                        onClick={() => del(g)}
                      />
                    </Tooltip>
                  </div>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}

      {/* 添加确认对话框 */}
      <Dialog
        open={add.open}
        onOpenChange={(_, d) => {
          if (!d.open) setAdd(emptyAdd)
        }}
      >
        <DialogSurface style={{ maxWidth: 720 }}>
          <DialogBody>
            <DialogTitle>添加程序</DialogTitle>
            <DialogContent>
              {add.loading ? (
                <div style={{ display: 'flex', gap: 12, alignItems: 'center', padding: 24 }}>
                  <Spinner size="tiny" /> <Text>正在解析…</Text>
                </div>
              ) : (
                <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
                  {add.result?.steam ? (
                    <div
                      style={{
                        padding: '12px 14px',
                        borderRadius: 8,
                        background: tokens.colorNeutralBackground2,
                        border: `1px solid ${tokens.colorNeutralStroke2}`,
                      }}
                    >
                      <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
                        <Badge appearance="filled" color="brand" size="small">
                          Steam
                        </Badge>
                        <Text weight="semibold">{add.steamName || '未知游戏'}</Text>
                        <Text size={200} style={{ color: tokens.colorNeutralForeground3 }}>
                          appid {add.appid}
                        </Text>
                      </div>
                      <div className={s.path} style={{ marginTop: 4 }}>
                        {add.result.steam.fullpath || '未找到安装目录'}
                      </div>
                    </div>
                  ) : null}

                  {add.result?.message ? (
                    <Text size={200} style={{ color: tokens.colorNeutralForeground2 }}>
                      {add.result.message}
                    </Text>
                  ) : null}

                  {/* 候选 exe 列表（含图标与体积） */}
                  {cands.length > 1 ? (
                    <div>
                      <Divider style={{ marginBottom: 10 }}>
                        <Text size={200}>选择主程序</Text>
                      </Divider>
                      <div style={{ maxHeight: 300, overflowY: 'auto' }}>
                        {cands.map((c) => (
                          <div
                            key={c.path}
                            className={`${s.candRow} ${add.exe === c.path ? s.candRowSel : ''}`}
                            onClick={() => setAdd((a) => ({ ...a, exe: c.path }))}
                          >
                            {c.icon ? (
                              <img className={s.candIcon} src={c.icon} alt="" />
                            ) : (
                              <div className={s.candIcon} />
                            )}
                            <div className={s.candMeta}>
                              <div className={s.candName}>{c.name}</div>
                              <Text size={100} style={{ color: tokens.colorNeutralForeground3 }}>
                                {c.sizetext} · {c.reason}
                              </Text>
                            </div>
                            {c.preferred ? (
                              <Badge appearance="tint" color="success" size="small">
                                推荐
                              </Badge>
                            ) : null}
                            {add.exe === c.path ? <Checkmark24Regular /> : null}
                          </div>
                        ))}
                      </div>
                    </div>
                  ) : null}

                  <Field
                    label="程序文件"
                    validationMessage={add.exe ? undefined : '请选择或手动指定主程序 exe'}
                    validationState={add.exe ? 'none' : 'warning'}
                  >
                    <Input
                      value={add.exe}
                      onChange={(_, d) => setAdd((a) => ({ ...a, exe: d.value }))}
                      placeholder="C:\Games\Game\game.exe"
                    />
                  </Field>

                  <Field
                    label="显示名（OLED 上显示，纯英文，最多 16 字符）"
                    validationState={checkDisplay(add.display) ? 'error' : 'none'}
                    validationMessage={checkDisplay(add.display) || undefined}
                  >
                    <Input
                      value={add.display}
                      maxLength={DISPLAY_MAX + 8}
                      onChange={(_, d) => setAdd((a) => ({ ...a, display: d.value }))}
                      contentAfter={
                        <Text size={100} style={{ color: tokens.colorNeutralForeground3 }}>
                          {add.display.length}/{DISPLAY_MAX}
                        </Text>
                      }
                    />
                  </Field>

                  {add.error ? (
                    <Text style={{ color: tokens.colorPaletteRedForeground1 }}>{add.error}</Text>
                  ) : null}
                </div>
              )}
            </DialogContent>
            <DialogActions>
              <Button appearance="secondary" onClick={() => setAdd(emptyAdd)}>
                取消
              </Button>
              <Button
                appearance="primary"
                disabled={add.loading || !add.exe || !!checkDisplay(add.display)}
                onClick={submitAdd}
              >
                {busy === 'add' ? '添加中…' : '添加'}
              </Button>
            </DialogActions>
          </DialogBody>
        </DialogSurface>
      </Dialog>

      {/* ===================== 改名弹窗 ===================== */}
      <Dialog
        open={!!rename}
        onOpenChange={(_, d) => {
          if (!d.open) setRename(null)
        }}
      >
        <DialogSurface style={{ maxWidth: 460 }}>
          <DialogBody>
            <DialogTitle>修改显示名</DialogTitle>
            <DialogContent>
              <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
                <Text size={200} style={{ color: tokens.colorNeutralForeground3 }}>
                  OLED 一行只有 16 个字符宽，且字库为 ASCII，所以只能是纯英文且不超过 16 字符。
                </Text>
                <Field
                  label="显示名"
                  validationState={renameErr ? 'error' : 'none'}
                  validationMessage={renameErr || undefined}
                >
                  <Input
                    value={renameVal}
                    autoFocus
                    maxLength={DISPLAY_MAX + 8}
                    onChange={(_, d) => setRenameVal(d.value)}
                    onKeyDown={(e) => {
                      if (e.key === 'Enter' && !renameErr) submitRename()
                      if (e.key === 'Escape') setRename(null)
                    }}
                    contentAfter={
                      <Text size={100} style={{ color: tokens.colorNeutralForeground3 }}>
                        {renameVal.length}/{DISPLAY_MAX}
                      </Text>
                    }
                  />
                </Field>
                {rename ? <div className={s.path}>{rename.exe}</div> : null}
              </div>
            </DialogContent>
            <DialogActions>
              <Button appearance="secondary" onClick={() => setRename(null)}>
                取消
              </Button>
              <Button
                appearance="primary"
                disabled={!!renameErr || busy === rename?.id}
                onClick={submitRename}
              >
                保存
              </Button>
            </DialogActions>
          </DialogBody>
        </DialogSurface>
      </Dialog>
    </div>
  )
}
