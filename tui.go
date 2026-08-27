// Bubble Tea による TUI 画面。-plain を付けない場合の既定モード。
//
// 画面はモード無し: 常に prefix 入力を受け付け、Enter で切り替える。
// キーが入力に食われるため、操作は Ctrl 系キーに割り当てる。
//
// 監視ポーリングは TUI 主導。tea.Tick が一定間隔で tickMsg を投げ、
// それを受けて Poll を tea.Cmd（別 goroutine）で走らせ、結果を pollMsg で
// 受け取る。Watcher.Start は使わないので、監視用の goroutine は増えない。
package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// View のレイアウト。行数を固定して端末が縦に揺れないようにする。
const (
	indent     = 2   // 左余白
	headerRows = 5   // タイトル / prefix 行 / 入力欄 / メッセージ / 履歴見出し
	tuiFooter  = 1   // ヘルプ
	historyMax = 500 // 保持する履歴の上限（古いものから捨てる）
	minHeight  = 10  // WindowSizeMsg が来る前に使う仮の高さ
)

// sampleName は next の見本に使う、元ファイル名の代わりの文字列。
// 実際にはここに撮影されたファイル名が入る。
const sampleName = "image.png"

// nextName は次にリネームされる名前の見本を返す（例: 01_03_image.png）。
// 見本の部分は薄く出して、実際には撮影されたファイル名が入ることを示す。
func nextName(prefix string, seq int) string {
	return fmt.Sprintf("%s_%02d_%s", prefix, seq, dimStyle.Render(sampleName))
}

// prefix 行のボタンのレイアウト。View と当たり判定 (buttonAt) の両方がここを参照する。
//
// 当たり判定は altscreen 前提。altscreen が無いと View は端末の途中から描画されるのに
// MouseMsg.Y は端末全体の行番号になり、座標が常にズレる（runTUI の WithAltScreen 参照）。
const (
	prefixRow   = 1          // prefix 行は View の2行目（0 始まり）
	prefixLabel = "prefix: " // ボタンの開始桁を決めるので固定文字列にしておく
	buttonGap   = 1          // ボタンと prefix の間の隙間
)

// prefixButtons は prefix 末尾の数字を増減するボタン。
// 画面には [ - ] prefix [ + ] の順に並ぶ。
var prefixButtons = []struct {
	label string
	delta int
}{
	{"[ - ]", -1},
	{"[ + ]", +1},
}

// buttonX は i 番目のボタンの開始桁を返す。
// [ + ] は prefix の右側にあるため、位置は現在の prefix の表示幅で決まる。
func buttonX(i int, prefix string) int {
	x := indent + len(prefixLabel)
	if i == 0 {
		return x
	}
	return x + len(prefixButtons[0].label) + buttonGap + lipgloss.Width(prefix) + buttonGap
}

// buttonAt は (x, y) 上のボタン番号を返す。無ければ -1。
func buttonAt(x, y int, prefix string) int {
	if y != prefixRow {
		return -1
	}
	for i, b := range prefixButtons {
		if start := buttonX(i, prefix); x >= start && x < start+len(b.label) {
			return i
		}
	}
	return -1
}

// bumpPrefix は prefix 末尾の数字を delta だけ動かした文字列を返す。
// ゼロ埋めの桁数は保つ（01 → 02、09 → 10、099 → 100）。
// 末尾が数字でなければ 01 を付ける（case → case01）。1 より下には減らさない。
// エラーの文面はそのまま画面に出るので、内部用ではなくユーザー向けに書く。
func bumpPrefix(p string, delta int) (string, error) {
	i := len(p)
	for i > 0 && p[i-1] >= '0' && p[i-1] <= '9' {
		i--
	}
	stem, digits := p[:i], p[i:]

	if digits == "" {
		if delta < 0 {
			return "", fmt.Errorf("no trailing number to decrease")
		}
		return p + "01", nil
	}

	n, err := strconv.Atoi(digits)
	if err != nil {
		return "", fmt.Errorf("number is too large")
	}
	n += delta
	if n < 1 {
		return "", fmt.Errorf("cannot go below 1")
	}
	return fmt.Sprintf("%s%0*d", stem, len(digits), n), nil
}

var (
	titleStyle  = lipgloss.NewStyle().Bold(true)
	hoverStyle  = lipgloss.NewStyle().Reverse(true)
	dimStyle    = lipgloss.NewStyle().Faint(true)
	okStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	errStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	pausedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
)

// tickMsg はポーリングの時刻が来たことを知らせる。
type tickMsg time.Time

// pollMsg は Poll 1回分の結果。
type pollMsg struct {
	results []Rename
	err     error
}

// histItem は履歴1行分。選択状態を行そのものに持たせているので、
// 新しいファイルが来て履歴が伸びても選択がずれない。
type histItem struct {
	Rename
	sel bool
}

type tuiModel struct {
	w        *Watcher
	folder   string
	interval time.Duration

	input   string     // 入力中の prefix またはコマンド
	history []histItem // 新しい順
	message string     // 直近の通知・エラー（1行）
	isErr   bool       // message がエラーか
	paused  bool
	polling bool // Poll が実行中（重複起動を防ぐ）
	hover   int  // マウスが乗っているボタン番号、無ければ -1

	height int
}

func newTUIModel(w *Watcher, folder string, interval time.Duration) tuiModel {
	return tuiModel{w: w, folder: folder, interval: interval, height: minHeight, hover: -1}
}

// historyRows は履歴に使える行数。
func (m tuiModel) historyRows() int {
	n := m.height - headerRows - tuiFooter
	if n < 1 {
		return 1
	}
	return n
}

// historyTop は履歴の1行目の行番号（View の先頭を 0 とする）。
const historyTop = headerRows

// historyRowAt は画面上の y 行目にある履歴のインデックスを返す。無ければ -1。
func (m tuiModel) historyRowAt(y int) int {
	if y < historyTop || y >= historyTop+m.historyRows() {
		return -1
	}
	i := y - historyTop
	if i >= len(m.history) {
		return -1
	}
	return i
}

// selectedCount は選択中の件数。
func (m tuiModel) selectedCount() int {
	n := 0
	for _, h := range m.history {
		if h.sel {
			n++
		}
	}
	return n
}

// selectedIndexes は選択中の履歴の位置を新しい順で返す。
func (m tuiModel) selectedIndexes() []int {
	var idx []int
	for i, h := range m.history {
		if h.sel {
			idx = append(idx, i)
		}
	}
	return idx
}

// toggleSelection は i 行目の選択を反転する。
//
// Update は tuiModel の値コピーを受け取るが、スライスの中身は共有しているため、
// 直接書き換えると前の状態まで変わってしまう。選択を触る時はスライスを複製する。
func (m tuiModel) toggleSelection(i int) tuiModel {
	hist := make([]histItem, len(m.history))
	copy(hist, m.history)
	hist[i].sel = !hist[i].sel
	m.history = hist
	return m
}

// clearSelection は選択を全て外す。
func (m tuiModel) clearSelection() tuiModel {
	hist := make([]histItem, len(m.history))
	copy(hist, m.history)
	for i := range hist {
		hist[i].sel = false
	}
	m.history = hist
	return m
}

func tickCmd(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func pollCmd(w *Watcher) tea.Cmd {
	return func() tea.Msg {
		results, err := w.Poll()
		return pollMsg{results: results, err: err}
	}
}

func (m tuiModel) Init() tea.Cmd {
	return tickCmd(m.interval)
}

func (m tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.height = msg.Height

	case tickMsg:
		// 一時停止中や前回の Poll が終わっていない間は、次のティックだけ入れて見送る。
		if m.paused || m.polling {
			return m, tickCmd(m.interval)
		}
		m.polling = true
		return m, tea.Batch(pollCmd(m.w), tickCmd(m.interval))

	case pollMsg:
		m.polling = false
		if msg.err != nil {
			m.message, m.isErr = fmt.Sprintf("cannot read folder: %v", msg.err), true
			return m, nil
		}
		// 新しいものが上に来るよう、今回の結果を反転して履歴の先頭に積む
		// (Poll は ReadDir 順 = 連番の若い順に返す)
		newest := make([]histItem, 0, len(msg.results)+len(m.history))
		for i := len(msg.results) - 1; i >= 0; i-- {
			newest = append(newest, histItem{Rename: msg.results[i]})
		}
		m.history = append(newest, m.history...)
		if len(m.history) > historyMax {
			m.history = m.history[:historyMax]
		}

	case tea.KeyMsg:
		return m.handleKey(msg)

	case tea.MouseMsg:
		return m.handleMouse(msg)
	}
	return m, nil
}

// handleMouse は prefix 増減ボタンと、履歴行の選択を処理する。
func (m tuiModel) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	prefix, _ := m.w.Status()
	i := buttonAt(msg.X, msg.Y, prefix)
	m.hover = i

	press := msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft
	if !press {
		return m, nil
	}
	if i >= 0 {
		return m.bump(prefixButtons[i].delta), nil
	}

	// 履歴行のクリックで、その行の選択を1件ずつ切り替える。
	// 変え忘れが複数回あっても、必要な行だけ拾えるようにするため。
	if row := m.historyRowAt(msg.Y); row >= 0 {
		m = m.toggleSelection(row)
		// 選択の件数は入力欄の見出しに出るので、ここでは何も言わない
		m.message, m.isErr = "", false
	}
	return m, nil
}

// bump は prefix 末尾の数字を delta だけ動かす。連番は SetPrefix でリセットされる。
func (m tuiModel) bump(delta int) tuiModel {
	prefix, _ := m.w.Status()
	next, err := bumpPrefix(prefix, delta)
	if err != nil {
		// bumpPrefix のエラーはユーザー向けの文面
		m.message, m.isErr = err.Error(), true
		return m
	}
	// 変わった prefix と連番はヘッダに出るので、成功時は何も言わない
	m.w.SetPrefix(next)
	m.message, m.isErr = "", false
	return m
}

func (m tuiModel) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyCtrlC:
		return m, tea.Quit
	case tea.KeyCtrlP:
		// 停止中かどうかはヘッダに出るので通知しない
		m.paused = !m.paused
		m.message, m.isErr = "", false
	case tea.KeyEsc:
		m = m.clearSelection()
		m.message, m.isErr = "", false
	case tea.KeyCtrlU:
		m.input = ""
	case tea.KeyBackspace:
		if r := []rune(m.input); len(r) > 0 {
			m.input = string(r[:len(r)-1])
		}
	case tea.KeyEnter:
		return m.applyInput(), nil
	case tea.KeySpace:
		m.input += " "
	case tea.KeyRunes:
		m.input += string(msg.Runes)
	}
	return m, nil
}

// applyInput は入力欄の内容を確定する。
// `:` 始まりはコマンド、履歴を選択中なら付け替え、それ以外は prefix の切り替え。
func (m tuiModel) applyInput() tuiModel {
	input := strings.TrimSpace(m.input)
	if input == "" {
		return m
	}
	m.input = ""

	if after, ok := strings.CutPrefix(input, ":"); ok {
		return m.runCommand(strings.Fields(after))
	}

	if err := validatePrefix(input); err != nil {
		// validatePrefix の文面は -plain 用の英語なので、画面には出さない
		m.message, m.isErr = "prefix cannot contain / or \\", true
		return m
	}

	if m.selectedCount() > 0 {
		return m.retag(input)
	}

	m.w.SetPrefix(input)
	m.message, m.isErr = "", false
	return m
}

// retag は選択中の履歴を新しい prefix で付け直す。連番は古い順に 01 から。
//
// 選択に最新の1件が含まれている場合だけ、これからの撮影もその prefix に切り替える
// （連番は付け替えた分の続きから）。途中だけを直した時に撮影中の prefix まで
// 巻き戻さないため。
func (m tuiModel) retag(prefix string) tuiModel {
	idx := m.selectedIndexes()
	items := make([]Rename, len(idx))
	for i, h := range idx {
		items[i] = m.history[h].Rename
	}

	updated, renamed, failed := m.w.Retag(items, prefix)

	hist := make([]histItem, len(m.history))
	copy(hist, m.history)
	for i, h := range idx {
		hist[h] = histItem{Rename: updated[i]}
	}
	m.history = hist

	// 付け替えた件数は他に出る場所が無いので伝える。prefix と連番はヘッダを見れば分かる。
	switch {
	case renamed > 0 && len(failed) > 0:
		m.message, m.isErr = fmt.Sprintf("renamed %d to %s (%d failed: %s)", renamed, prefix, len(failed), failed[0]), true
	case renamed > 0:
		m.message, m.isErr = fmt.Sprintf("renamed %d to %s", renamed, prefix), false
	case len(failed) > 0:
		m.message, m.isErr = fmt.Sprintf("could not rename %s", failed[0]), true
	default:
		m.message, m.isErr = "nothing to rename", true
	}

	// 選択に最新が含まれていた時だけ、これからの撮影もその prefix にする
	if renamed > 0 && len(idx) > 0 && idx[0] == 0 {
		m.w.SetPrefix(prefix)
		m.w.SetCounter(renamed + 1)
	}
	return m
}

// runCommand は `:` コマンドを処理する。増やす時はここに分岐を足す。
func (m tuiModel) runCommand(parts []string) tuiModel {
	m.isErr = true
	if len(parts) == 0 {
		m.message = "commands: :seq N"
		return m
	}
	switch parts[0] {
	case "seq":
		if len(parts) != 2 {
			m.message = ":seq takes one number, e.g. :seq 5"
			return m
		}
		n, err := strconv.Atoi(parts[1])
		if err != nil || n < 1 {
			m.message = "next must be 1 or more"
			return m
		}
		m.w.SetCounter(n)
		m.message, m.isErr = "", false // 連番はヘッダに出る
	default:
		m.message = "commands: :seq N"
	}
	return m
}

// renameLine は履歴1行分の表示。失敗した分は理由を出す。
func renameLine(r Rename) string {
	if r.Err != nil {
		return errStyle.Render(fmt.Sprintf("✗ %s  rename failed: %v", r.Old, r.Err))
	}
	return fmt.Sprintf("%s  %s", r.New, dimStyle.Render("← "+r.Old))
}

// renderButton は i 番目のボタンを描く。マウスが乗っていれば反転させる。
func (m tuiModel) renderButton(i int) string {
	if i == m.hover {
		return hoverStyle.Render(prefixButtons[i].label)
	}
	return prefixButtons[i].label
}

// promptLabel は入力欄の見出し。履歴を選択中は付け替えだと分かるようにする。
func (m tuiModel) promptLabel() string {
	if n := m.selectedCount(); n > 0 {
		return fmt.Sprintf("prefix (%d selected)> ", n)
	}
	return "prefix> "
}

func (m tuiModel) View() string {
	pad := strings.Repeat(" ", indent)

	state := okStyle.Render("● watching")
	if m.paused {
		state = pausedStyle.Render("‖ paused")
	}
	prefix, nextSeq := m.w.Status()
	gap := strings.Repeat(" ", buttonGap)

	msg := m.message
	if m.isErr && msg != "" {
		msg = errStyle.Render(msg)
	}

	// 段組みは headerRows / tuiFooter と対応させる。行がずれると
	// ボタンと履歴行の当たり判定が狂うので、行数を増やす時は定数も直す。
	lines := make([]string, 0, m.height)
	lines = append(lines,
		fmt.Sprintf("%s%s    %s    %s", pad, titleStyle.Render("capture-renamer"), m.folder, state),
		// buttonX と桁がずれないよう、prefix 行はこの順・この隙間で組み立てる
		// 80桁の端末でも収まるよう、項目の区切りは3スペースに詰めている
		fmt.Sprintf("%s%s%s%s%s%s%s   next: %02d   preview: %s   interval: %v",
			pad, prefixLabel,
			m.renderButton(0), gap, titleStyle.Render(prefix), gap, m.renderButton(1),
			nextSeq, nextName(prefix, nextSeq), m.interval),
		fmt.Sprintf("%s%s%s_", pad, m.promptLabel(), m.input),
		msgLine(pad, msg),
		pad+dimStyle.Render(fmt.Sprintf("history (%d)", len(m.history))),
	)

	for i := 0; i < m.historyRows(); i++ {
		if i >= len(m.history) {
			lines = append(lines, "") // 余った行も空行で埋めて行数を一定に保つ
			continue
		}
		marker := "  "
		if m.history[i].sel {
			marker = "▸ "
		}
		lines = append(lines, pad+marker+renameLine(m.history[i].Rename))
	}

	lines = append(lines, pad+dimStyle.Render("[ - ]/[ + ] step   click rows, then prefix + Enter   Esc clear   Ctrl+P pause   Ctrl+C quit"))

	// 端末より高いフレームを返すと Bubble Tea が「上から」行を捨てるため
	// (standard_renderer.go の flush)、全部の行番号が上にずれてボタンの当たり判定が
	// 狂う。末尾に改行を足さないこと、行数を端末の高さ以内に収めることの両方が必要。
	// 高さが足りない時は下を切って、上の行の位置を守る。
	if len(lines) > m.height {
		lines = lines[:m.height]
	}
	return strings.Join(lines, "\n")
}

// runTUI は TUI モードで監視を実行する。
func runTUI(w *Watcher, folder string, interval time.Duration) error {
	// WithAltScreen はボタンの当たり判定に必須（buttonAt のコメント参照）。
	// ホバー反転にはモーション通知が要るので WithMouseAllMotion を使う。
	p := tea.NewProgram(newTUIModel(w, folder, interval), tea.WithAltScreen(), tea.WithMouseAllMotion())
	_, err := p.Run()
	return err
}

// msgLine はメッセージ行。空の時は余白だけの行にしない。
func msgLine(pad, msg string) string {
	if msg == "" {
		return ""
	}
	return pad + msg
}
