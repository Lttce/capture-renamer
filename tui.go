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
	headerRows = 4   // タイトル / 状態 / 空行 / 履歴見出し
	tuiFooter  = 4   // 空行 / メッセージ / 入力欄 / ヘルプ
	historyMax = 500 // 保持する履歴の上限（古いものから捨てる）
	minHeight  = 10  // WindowSizeMsg が来る前に使う仮の高さ
)

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
func bumpPrefix(p string, delta int) (string, error) {
	i := len(p)
	for i > 0 && p[i-1] >= '0' && p[i-1] <= '9' {
		i--
	}
	stem, digits := p[:i], p[i:]

	if digits == "" {
		if delta < 0 {
			return "", fmt.Errorf("%q には減らせる数字がありません", p)
		}
		return p + "01", nil
	}

	n, err := strconv.Atoi(digits)
	if err != nil {
		return "", fmt.Errorf("%q の数字が大きすぎます", p)
	}
	n += delta
	if n < 1 {
		return "", fmt.Errorf("これ以上減らせません")
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

type tuiModel struct {
	w        *Watcher
	folder   string
	interval time.Duration

	input   string   // 入力中の prefix またはコマンド
	history []Rename // 新しい順
	message string   // 直近の通知・エラー（1行）
	isErr   bool     // message がエラーか
	paused  bool
	polling bool // Poll が実行中（重複起動を防ぐ）
	hover   int  // マウスが乗っているボタン番号、無ければ -1
	height  int
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
			m.message, m.isErr = fmt.Sprintf("監視エラー: %v", msg.err), true
			return m, nil
		}
		// 新しいものが上に来るよう、今回の結果を反転して履歴の先頭に積む
		// (Poll は ReadDir 順 = 連番の若い順に返す)
		newest := make([]Rename, 0, len(msg.results)+len(m.history))
		for i := len(msg.results) - 1; i >= 0; i-- {
			newest = append(newest, msg.results[i])
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

// handleMouse は prefix 増減ボタンのホバーとクリックを処理する。
func (m tuiModel) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	prefix, _ := m.w.Status()
	i := buttonAt(msg.X, msg.Y, prefix)
	m.hover = i
	if i >= 0 && msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
		return m.bump(prefixButtons[i].delta), nil
	}
	return m, nil
}

// bump は prefix 末尾の数字を delta だけ動かす。連番は SetPrefix でリセットされる。
func (m tuiModel) bump(delta int) tuiModel {
	prefix, _ := m.w.Status()
	next, err := bumpPrefix(prefix, delta)
	if err != nil {
		m.message, m.isErr = err.Error(), true
		return m
	}
	m.w.SetPrefix(next)
	m.message, m.isErr = fmt.Sprintf("prefix を %q に変更、連番をリセット", next), false
	return m
}

func (m tuiModel) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyCtrlC:
		return m, tea.Quit
	case tea.KeyCtrlP:
		m.paused = !m.paused
		if m.paused {
			m.message, m.isErr = "一時停止中（Ctrl+P で再開）", false
		} else {
			m.message, m.isErr = "監視を再開しました", false
		}
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

// applyInput は入力欄の内容を確定する。`:` 始まりはコマンド、それ以外は prefix。
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
		m.message, m.isErr = err.Error(), true
		return m
	}
	m.w.SetPrefix(input)
	m.message, m.isErr = fmt.Sprintf("prefix を %q に変更、連番をリセット", input), false
	return m
}

// runCommand は `:` コマンドを処理する。増やす時はここに分岐を足す。
func (m tuiModel) runCommand(parts []string) tuiModel {
	m.isErr = true
	if len(parts) == 0 {
		m.message = "使えるコマンド: :seq N"
		return m
	}
	switch parts[0] {
	case "seq":
		if len(parts) != 2 {
			m.message = "usage: :seq <N>"
			return m
		}
		n, err := strconv.Atoi(parts[1])
		if err != nil || n < 1 {
			m.message = "連番は1以上の整数で指定してください"
			return m
		}
		m.w.SetCounter(n)
		m.message, m.isErr = fmt.Sprintf("連番を %d に設定", n), false
	default:
		m.message = fmt.Sprintf("不明なコマンド %q（使えるコマンド: :seq N）", parts[0])
	}
	return m
}

// renameLine は履歴1行分の表示。失敗した分は理由を出す。
func renameLine(r Rename) string {
	if r.Err != nil {
		return errStyle.Render(fmt.Sprintf("✗ %s  リネーム失敗: %v", r.Old, r.Err))
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

func (m tuiModel) View() string {
	var b strings.Builder
	pad := strings.Repeat(" ", indent)

	state := okStyle.Render("● 監視中")
	if m.paused {
		state = pausedStyle.Render("‖ 一時停止")
	}
	prefix, nextSeq := m.w.Status()

	fmt.Fprintf(&b, "%s%s    %s    %s\n", pad, titleStyle.Render("capture-renamer"), m.folder, state)
	// buttonX と桁がずれないよう、prefix 行はこの順・この隙間で組み立てる
	gap := strings.Repeat(" ", buttonGap)
	fmt.Fprintf(&b, "%s%s%s%s%s%s%s     次の連番: %02d    間隔: %v\n\n",
		pad, prefixLabel,
		m.renderButton(0), gap, titleStyle.Render(prefix), gap, m.renderButton(1),
		nextSeq, m.interval)
	fmt.Fprintf(&b, "%s%s\n", pad, dimStyle.Render(fmt.Sprintf("履歴 (%d)", len(m.history))))

	rows := m.historyRows()
	for i := 0; i < rows; i++ {
		if i >= len(m.history) {
			b.WriteString("\n") // 余った行も空行で埋めて View の行数を一定に保つ
			continue
		}
		b.WriteString(pad + renameLine(m.history[i]) + "\n")
	}

	msg := m.message
	if m.isErr && msg != "" {
		msg = errStyle.Render(msg)
	}
	fmt.Fprintf(&b, "\n%s%s\n", pad, msg)
	fmt.Fprintf(&b, "%s新しい prefix> %s_\n", pad, m.input)
	fmt.Fprintf(&b, "%s%s\n", pad, dimStyle.Render("[ - ]/[ + ] クリックで番号送り   Enter 確定   :seq N 連番指定   Ctrl+U 消去   Ctrl+P 停止   Ctrl+C 終了"))
	return b.String()
}

// runTUI は TUI モードで監視を実行する。
func runTUI(w *Watcher, folder string, interval time.Duration) error {
	// WithAltScreen はボタンの当たり判定に必須（buttonAt のコメント参照）。
	// ホバー反転にはモーション通知が要るので WithMouseAllMotion を使う。
	p := tea.NewProgram(newTUIModel(w, folder, interval), tea.WithAltScreen(), tea.WithMouseAllMotion())
	_, err := p.Run()
	return err
}
