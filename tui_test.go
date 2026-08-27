// TUI モデルのユニットテスト。
// 実際の描画とキー操作の確認は端末上で手動で行う（Program.Run はここでは回さない）。
package main

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const testInterval = 10 * time.Millisecond

func newTestTUI(t *testing.T) (tuiModel, string) {
	t.Helper()
	dir := t.TempDir()
	w := NewWatcher(dir, "test")
	if err := w.ScanExisting(); err != nil {
		t.Fatal(err)
	}
	m := newTUIModel(w, dir, testInterval)
	m.height = 20
	return m, dir
}

func update(t *testing.T, m tuiModel, msg tea.Msg) (tuiModel, tea.Cmd) {
	t.Helper()
	next, cmd := m.Update(msg)
	got, ok := next.(tuiModel)
	if !ok {
		t.Fatalf("Update returned %T, want tuiModel", next)
	}
	return got, cmd
}

// typeText は文字列をキー入力として流し込む。
func typeText(t *testing.T, m tuiModel, s string) tuiModel {
	t.Helper()
	for _, r := range s {
		key := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}
		if r == ' ' {
			key = tea.KeyMsg{Type: tea.KeySpace}
		}
		m, _ = update(t, m, key)
	}
	return m
}

func enter(t *testing.T, m tuiModel) tuiModel {
	t.Helper()
	m, _ = update(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	return m
}

var ansi = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// plainView は色付けを取り除いた View。端末プロファイル差で落ちないようにする。
func plainView(m tuiModel) string {
	return ansi.ReplaceAllString(m.View(), "")
}

func TestEnterChangesPrefixAndResetsCounter(t *testing.T) {
	m, _ := newTestTUI(t)
	m.w.SetCounter(7)

	m = enter(t, typeText(t, m, "shot01"))

	prefix, next := m.w.Status()
	if prefix != "shot01" || next != 1 {
		t.Errorf("prefix=%q next=%d, want shot01 / 1", prefix, next)
	}
	if m.input != "" {
		t.Errorf("input = %q, want cleared", m.input)
	}
	if m.isErr || !strings.Contains(m.message, "shot01") {
		t.Errorf("message = %q (isErr=%v), want a notice mentioning shot01", m.message, m.isErr)
	}
}

func TestEmptyInputIsIgnored(t *testing.T) {
	m, _ := newTestTUI(t)
	m = enter(t, typeText(t, m, "   "))
	if prefix, _ := m.w.Status(); prefix != "test" {
		t.Errorf("prefix = %q, want unchanged", prefix)
	}
	if m.message != "" {
		t.Errorf("message = %q, want empty", m.message)
	}
}

func TestInvalidPrefixKeepsCurrentPrefix(t *testing.T) {
	m, _ := newTestTUI(t)
	m = enter(t, typeText(t, m, "a/b"))
	if prefix, _ := m.w.Status(); prefix != "test" {
		t.Errorf("prefix = %q, want unchanged", prefix)
	}
	if !m.isErr || m.message == "" {
		t.Errorf("expected an error message, got %q (isErr=%v)", m.message, m.isErr)
	}
}

func TestSeqCommand(t *testing.T) {
	m, _ := newTestTUI(t)
	m = enter(t, typeText(t, m, ":seq 5"))
	if _, next := m.w.Status(); next != 5 {
		t.Errorf("next seq = %d, want 5", next)
	}
	if m.isErr {
		t.Errorf("message = %q, want a non-error notice", m.message)
	}
	// prefix は変えない
	if prefix, _ := m.w.Status(); prefix != "test" {
		t.Errorf("prefix = %q, want unchanged", prefix)
	}
}

func TestBadCommands(t *testing.T) {
	for _, input := range []string{":seq", ":seq x", ":seq 0", ":seq 1 2", ":bogus", ":"} {
		m, _ := newTestTUI(t)
		m = enter(t, typeText(t, m, input))
		if !m.isErr || m.message == "" {
			t.Errorf("%q: expected an error message, got %q (isErr=%v)", input, m.message, m.isErr)
		}
		if _, next := m.w.Status(); next != 1 {
			t.Errorf("%q: next seq = %d, want 1 (unchanged)", input, next)
		}
	}
}

func TestBackspaceAndClear(t *testing.T) {
	m, _ := newTestTUI(t)
	m = typeText(t, m, "abc")
	m, _ = update(t, m, tea.KeyMsg{Type: tea.KeyBackspace})
	if m.input != "ab" {
		t.Errorf("input = %q, want ab", m.input)
	}
	m, _ = update(t, m, tea.KeyMsg{Type: tea.KeyCtrlU})
	if m.input != "" {
		t.Errorf("input = %q, want cleared", m.input)
	}
	// 空の入力欄で backspace を押しても壊れない
	m, _ = update(t, m, tea.KeyMsg{Type: tea.KeyBackspace})
	if m.input != "" {
		t.Errorf("input = %q, want cleared", m.input)
	}
}

// tickMsg は Poll と次のティックの2本を返す。
func TestTickStartsPoll(t *testing.T) {
	m, _ := newTestTUI(t)
	m, cmd := update(t, m, tickMsg(time.Now()))
	if !m.polling {
		t.Error("polling should be true after a tick")
	}
	batch, ok := cmd().(tea.BatchMsg)
	if !ok {
		t.Fatalf("cmd() = %T, want tea.BatchMsg (poll + next tick)", cmd())
	}
	if len(batch) != 2 {
		t.Errorf("batch has %d commands, want 2", len(batch))
	}
}

// 一時停止中と Poll 実行中は、次のティックだけ入れて Poll を見送る。
func TestTickSkipsPollWhenPausedOrBusy(t *testing.T) {
	base, _ := newTestTUI(t)

	paused, _ := update(t, base, tea.KeyMsg{Type: tea.KeyCtrlP})
	if !paused.paused {
		t.Fatal("Ctrl+P should pause")
	}
	m, cmd := update(t, paused, tickMsg(time.Now()))
	if m.polling {
		t.Error("paused tick should not start a poll")
	}
	if _, ok := cmd().(tickMsg); !ok {
		t.Errorf("cmd() = %T, want tickMsg only", cmd())
	}

	busy := base
	busy.polling = true
	m, cmd = update(t, busy, tickMsg(time.Now()))
	if _, ok := cmd().(tickMsg); !ok {
		t.Errorf("busy: cmd() = %T, want tickMsg only", cmd())
	}

	// Ctrl+P をもう一度押すと再開する
	resumed, _ := update(t, paused, tea.KeyMsg{Type: tea.KeyCtrlP})
	if resumed.paused {
		t.Error("second Ctrl+P should resume")
	}
	if m, _ = update(t, resumed, tickMsg(time.Now())); !m.polling {
		t.Error("resumed tick should start a poll")
	}
}

func TestPollMsgKeepsNewestFirst(t *testing.T) {
	m, _ := newTestTUI(t)
	m, _ = update(t, m, pollMsg{results: []Rename{
		{Old: "a.png", New: "test_01_a.png"},
		{Old: "b.png", New: "test_02_b.png"},
	}})
	m, _ = update(t, m, pollMsg{results: []Rename{{Old: "c.png", New: "test_03_c.png"}}})

	if m.polling {
		t.Error("polling should be false after pollMsg")
	}
	want := []string{"test_03_c.png", "test_02_b.png", "test_01_a.png"}
	if len(m.history) != len(want) {
		t.Fatalf("history has %d entries, want %d", len(m.history), len(want))
	}
	for i, w := range want {
		if m.history[i].New != w {
			t.Errorf("history[%d] = %q, want %q", i, m.history[i].New, w)
		}
	}
}

func TestHistoryIsCapped(t *testing.T) {
	m, _ := newTestTUI(t)
	results := make([]Rename, historyMax+10)
	for i := range results {
		results[i] = Rename{Old: fmt.Sprintf("%d.png", i), New: fmt.Sprintf("test_%d.png", i)}
	}
	m, _ = update(t, m, pollMsg{results: results})
	if len(m.history) != historyMax {
		t.Errorf("history has %d entries, want %d", len(m.history), historyMax)
	}
	// 直近の分（最後に処理された = 先頭）が残っている
	if m.history[0].New != fmt.Sprintf("test_%d.png", len(results)-1) {
		t.Errorf("history[0] = %q, want the newest entry", m.history[0].New)
	}
}

func TestPollErrorShowsMessage(t *testing.T) {
	m, _ := newTestTUI(t)
	m.polling = true
	m, _ = update(t, m, pollMsg{err: fmt.Errorf("boom")})
	if m.polling {
		t.Error("polling should be false after an error")
	}
	if !m.isErr || !strings.Contains(m.message, "boom") {
		t.Errorf("message = %q (isErr=%v), want it to mention boom", m.message, m.isErr)
	}
}

func TestFailedRenameIsShown(t *testing.T) {
	m, _ := newTestTUI(t)
	m, _ = update(t, m, pollMsg{results: []Rename{{Old: "locked.png", Err: fmt.Errorf("permission denied")}}})
	view := plainView(m)
	if !strings.Contains(view, "locked.png") || !strings.Contains(view, "permission denied") {
		t.Errorf("View should show the failed rename:\n%s", view)
	}
}

func TestViewShowsStatus(t *testing.T) {
	m, dir := newTestTUI(t)
	m.w.SetPrefix("shot")
	m.w.SetCounter(3)
	view := plainView(m)
	for _, want := range []string{"capture-renamer", dir, "shot", "次の連番: 03", "監視中", "10ms"} {
		if !strings.Contains(view, want) {
			t.Errorf("View should contain %q:\n%s", want, view)
		}
	}
	paused, _ := update(t, m, tea.KeyMsg{Type: tea.KeyCtrlP})
	if !strings.Contains(plainView(paused), "一時停止") {
		t.Errorf("paused View should say 一時停止:\n%s", plainView(paused))
	}
}

// 履歴の件数や入力内容が変わっても View の行数は一定（画面が縦に揺れない）。
func TestViewHeightIsStable(t *testing.T) {
	base, _ := newTestTUI(t)
	want := len(strings.Split(base.View(), "\n"))

	for _, n := range []int{1, 5, 100, historyMax + 10} {
		m := base
		results := make([]Rename, n)
		for i := range results {
			results[i] = Rename{Old: fmt.Sprintf("%d.png", i), New: fmt.Sprintf("test_%d.png", i)}
		}
		m, _ = update(t, m, pollMsg{results: results})
		m = typeText(t, m, "typing")
		m, _ = update(t, m, pollMsg{err: fmt.Errorf("boom")})
		if got := len(strings.Split(m.View(), "\n")); got != want {
			t.Errorf("%d history entries: View has %d lines, want %d", n, got, want)
		}
	}
}

func TestHistoryRowsFollowsWindowSize(t *testing.T) {
	m, _ := newTestTUI(t)
	m, _ = update(t, m, tea.WindowSizeMsg{Width: 80, Height: 30})
	if got, want := m.historyRows(), 30-headerRows-tuiFooter; got != want {
		t.Errorf("historyRows = %d, want %d", got, want)
	}
	// 極端に狭い端末でも1行は確保する
	m, _ = update(t, m, tea.WindowSizeMsg{Width: 80, Height: 3})
	if got := m.historyRows(); got != 1 {
		t.Errorf("historyRows = %d, want 1", got)
	}
}

func TestCtrlCQuits(t *testing.T) {
	m, _ := newTestTUI(t)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil {
		t.Fatal("Ctrl+C should return a quit command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Errorf("cmd() = %T, want tea.QuitMsg", cmd())
	}
}

// pollCmd が実際にリネームを行い、その結果が履歴に載るところまで通す。
func TestPollCmdRenamesAndLandsInHistory(t *testing.T) {
	m, dir := newTestTUI(t)
	createFile(t, dir, "shot.png")

	msg, ok := pollCmd(m.w)().(pollMsg)
	if !ok {
		t.Fatalf("pollCmd returned %T, want pollMsg", pollCmd(m.w)())
	}
	if msg.err != nil {
		t.Fatal(msg.err)
	}
	m, _ = update(t, m, msg)

	if len(m.history) != 1 || m.history[0].New != "test_01_shot.png" {
		t.Fatalf("history = %+v, want one test_01_shot.png entry", m.history)
	}
	if !fileExists(dir, "test_01_shot.png") {
		t.Errorf("renamed file missing: %v", dirNames(t, dir))
	}
	if !strings.Contains(plainView(m), "test_01_shot.png") {
		t.Errorf("View should show the rename:\n%s", plainView(m))
	}
}

// Init は最初のティックを仕掛ける。
func TestInitSchedulesTick(t *testing.T) {
	m, _ := newTestTUI(t)
	cmd := m.Init()
	if cmd == nil {
		t.Fatal("Init should schedule a tick")
	}
	if _, ok := cmd().(tickMsg); !ok {
		t.Errorf("cmd() = %T, want tickMsg", cmd())
	}
}

func TestBumpPrefix(t *testing.T) {
	for _, tc := range []struct {
		in    string
		delta int
		want  string
	}{
		{"01", +1, "02"},
		{"09", +1, "10"},
		{"099", +1, "100"},
		{"99", +1, "100"}, // 桁が増えるときは埋めた幅を超える
		{"1", +1, "2"},
		{"02", -1, "01"},
		{"10", -1, "09"},
		{"100", -1, "099"},
		{"case01", +1, "case02"},
		{"case09", +1, "case10"},
		{"test", +1, "test01"}, // 末尾に数字が無ければ 01 を付ける
	} {
		got, err := bumpPrefix(tc.in, tc.delta)
		if err != nil {
			t.Errorf("bumpPrefix(%q, %d): %v", tc.in, tc.delta, err)
			continue
		}
		if got != tc.want {
			t.Errorf("bumpPrefix(%q, %d) = %q, want %q", tc.in, tc.delta, got, tc.want)
		}
	}
}

func TestBumpPrefixErrors(t *testing.T) {
	for _, tc := range []struct{ in string }{{"01"}, {"1"}, {"case01"}} {
		if got, err := bumpPrefix(tc.in, -1); err == nil {
			t.Errorf("bumpPrefix(%q, -1) = %q, want an error (1 より下には減らさない)", tc.in, got)
		}
	}
	if got, err := bumpPrefix("test", -1); err == nil {
		t.Errorf("bumpPrefix(\"test\", -1) = %q, want an error (減らせる数字がない)", got)
	}
}

// ボタンのクリックで prefix が増減し、連番がリセットされる。
func TestButtonClickBumpsPrefix(t *testing.T) {
	m, _ := newTestTUI(t)
	m.w.SetPrefix("01")
	m.w.SetCounter(9)

	m = clickButton(t, m, 1) // [ + ]
	if prefix, next := m.w.Status(); prefix != "02" || next != 1 {
		t.Errorf("after [ + ]: prefix=%q next=%d, want 02 / 1", prefix, next)
	}
	m = clickButton(t, m, 0) // [ - ]
	if prefix, _ := m.w.Status(); prefix != "01" {
		t.Errorf("after [ - ]: prefix=%q, want 01", prefix)
	}
	// 01 からは減らせないので、prefix はそのままでエラーメッセージが出る
	m = clickButton(t, m, 0)
	if prefix, _ := m.w.Status(); prefix != "01" {
		t.Errorf("prefix = %q, want 01 (unchanged)", prefix)
	}
	if !m.isErr || m.message == "" {
		t.Errorf("expected an error message, got %q (isErr=%v)", m.message, m.isErr)
	}
}

// clickButton は i 番目のボタンの左端を左クリックする。
func clickButton(t *testing.T, m tuiModel, i int) tuiModel {
	t.Helper()
	prefix, _ := m.w.Status()
	m, _ = update(t, m, tea.MouseMsg{
		X:      buttonX(i, prefix),
		Y:      prefixRow,
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
	})
	return m
}

func TestClickOutsideButtonsDoesNothing(t *testing.T) {
	m, _ := newTestTUI(t)
	m.w.SetPrefix("01")
	prefix, _ := m.w.Status()

	for _, pos := range []struct {
		name string
		x, y int
	}{
		{"左余白", 0, prefixRow},
		{"ボタンの1桁左", buttonX(0, prefix) - 1, prefixRow},
		{"prefix 自体", buttonX(0, prefix) + len(prefixButtons[0].label) + buttonGap, prefixRow},
		{"別の行", buttonX(1, prefix), prefixRow + 1},
	} {
		got, _ := update(t, m, tea.MouseMsg{X: pos.x, Y: pos.y, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
		if p, _ := got.w.Status(); p != "01" {
			t.Errorf("%s のクリックで prefix が %q になった、want 01", pos.name, p)
		}
		if got.hover != -1 {
			t.Errorf("%s: hover = %d, want -1", pos.name, got.hover)
		}
	}
}

func TestHoverHighlightsButton(t *testing.T) {
	m, _ := newTestTUI(t)
	m.w.SetPrefix("01")
	prefix, _ := m.w.Status()

	m, _ = update(t, m, tea.MouseMsg{X: buttonX(1, prefix), Y: prefixRow, Action: tea.MouseActionMotion})
	if m.hover != 1 {
		t.Fatalf("hover = %d, want 1", m.hover)
	}
	if !strings.Contains(m.View(), hoverStyle.Render(prefixButtons[1].label)) {
		t.Error("ホバー中のボタンが反転していない")
	}
	// ボタンから外れたら反転も外れる
	m, _ = update(t, m, tea.MouseMsg{X: 0, Y: prefixRow, Action: tea.MouseActionMotion})
	if m.hover != -1 {
		t.Errorf("hover = %d, want -1", m.hover)
	}
}

// View 上のボタンの桁が buttonX と一致していること（当たり判定の要）。
// prefix の幅が変わっても [ + ] の位置が追従することを確かめる。
func TestButtonLayoutMatchesView(t *testing.T) {
	for _, prefix := range []string{"01", "099", "test", "case01", "日本語01"} {
		m, _ := newTestTUI(t)
		m.w.SetPrefix(prefix)

		lines := strings.Split(plainView(m), "\n")
		if len(lines) <= prefixRow {
			t.Fatalf("View has %d lines, want > %d", len(lines), prefixRow)
		}
		row := lines[prefixRow]
		for i, b := range prefixButtons {
			// strings.Index はバイト位置なので、表示桁 (= MouseMsg.X の単位) に換算する
			col := lipgloss.Width(row[:strings.Index(row, b.label)])
			if col != buttonX(i, prefix) {
				t.Errorf("prefix=%q: %s は View の %d 桁目、buttonX(%d) = %d (row=%q)",
					prefix, b.label, col, i, buttonX(i, prefix), row)
			}
			if got := buttonAt(col, prefixRow, prefix); got != i {
				t.Errorf("prefix=%q: buttonAt(%d) = %d, want %d", prefix, col, got, i)
			}
			// ボタンの右端の1桁も当たる／その1桁外は当たらない
			last := col + len(b.label) - 1
			if got := buttonAt(last, prefixRow, prefix); got != i {
				t.Errorf("prefix=%q: buttonAt(%d) = %d, want %d (右端)", prefix, last, got, i)
			}
			if got := buttonAt(last+1, prefixRow, prefix); got == i {
				t.Errorf("prefix=%q: buttonAt(%d) がまだ %d に当たっている", prefix, last+1, i)
			}
		}
	}
}
