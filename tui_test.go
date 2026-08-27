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
	// 成功時はメッセージを出さない（新しい prefix はヘッダに出る）
	if m.message != "" {
		t.Errorf("message = %q, want 空", m.message)
	}
	if !strings.Contains(plainView(m), "shot01") {
		t.Errorf("ヘッダに新しい prefix が出ていない:\n%s", plainView(m))
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

// View は端末の高さと同じ行数を、末尾の改行なしで返す。
//
// Bubble Tea の標準レンダラはフレームが端末より高いと「上から」行を捨てるため
// (standard_renderer.go の flush)、1行でも多いと全部の行番号がずれ、ボタンの
// 当たり判定がマウスの位置と合わなくなる。
func TestViewFitsTerminalHeight(t *testing.T) {
	for _, height := range []int{3, 8, 9, 10, 24, 50} {
		for _, entries := range []int{0, 2, 1000} {
			m, _ := newTestTUI(t)
			m, _ = update(t, m, tea.WindowSizeMsg{Width: 80, Height: height})
			results := make([]Rename, entries)
			for i := range results {
				results[i] = Rename{Old: fmt.Sprintf("%d.png", i), New: fmt.Sprintf("01_%d.png", i)}
			}
			m, _ = update(t, m, pollMsg{results: results})

			view := m.View()
			if got := len(strings.Split(view, "\n")); got != height {
				t.Errorf("height=%d entries=%d: View は %d 行、want %d 行", height, entries, got, height)
			}
			// フレームが収まる高さでは、最後の行はヘルプ行（末尾に余計な改行が無い）
			if height >= 9 && !strings.Contains(lastLine(plainView(m)), "Ctrl+C 終了") {
				t.Errorf("height=%d entries=%d: 最終行 = %q, want ヘルプ行", height, entries, lastLine(plainView(m)))
			}
		}
	}
}

// 画面の1行目はタイトル行、2行目はボタンのある prefix 行。
// この2つがずれると MouseMsg.Y と当たり判定が食い違う。
func TestViewRowOrder(t *testing.T) {
	m, _ := newTestTUI(t)
	m, _ = update(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	lines := strings.Split(plainView(m), "\n")

	if !strings.Contains(lines[0], "capture-renamer") {
		t.Errorf("1行目 = %q, want タイトル行", lines[0])
	}
	if !strings.Contains(lines[prefixRow], prefixButtons[0].label) {
		t.Errorf("%d 行目 = %q, want ボタンのある prefix 行", prefixRow, lines[prefixRow])
	}
}

// lastLine は文字列の最後の行を返す。
func lastLine(s string) string {
	lines := strings.Split(s, "\n")
	return lines[len(lines)-1]
}

// clickHistory は履歴の i 番目の行を左クリックする。
func clickHistory(t *testing.T, m tuiModel, i int) tuiModel {
	t.Helper()
	m, _ = update(t, m, tea.MouseMsg{
		X:      indent,
		Y:      historyTop + i,
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
	})
	return m
}

// withHistory は履歴を n 件持ったモデルを返す（新しい順に a{n-1} … a0）。
func withHistory(t *testing.T, n int) tuiModel {
	t.Helper()
	m, _ := newTestTUI(t)
	results := make([]Rename, n)
	for i := range results {
		results[i] = Rename{Old: fmt.Sprintf("a%d.png", i), New: fmt.Sprintf("01_%02d_a%d.png", i+1, i)}
	}
	m, _ = update(t, m, pollMsg{results: results})
	return m
}

func TestHistoryRowAt(t *testing.T) {
	m := withHistory(t, 3)
	for i := 0; i < 3; i++ {
		if got := m.historyRowAt(historyTop + i); got != i {
			t.Errorf("historyRowAt(%d) = %d, want %d", historyTop+i, got, i)
		}
	}
	for _, y := range []int{0, prefixRow, historyTop - 1, historyTop + 3, historyTop + m.historyRows()} {
		if got := m.historyRowAt(y); got != -1 {
			t.Errorf("historyRowAt(%d) = %d, want -1", y, got)
		}
	}
}

// 履歴の行はクリックするたびに1件ずつ選択・解除できる。
func TestClickTogglesRowSelection(t *testing.T) {
	m := withHistory(t, 5)

	m = clickHistory(t, m, 2)
	m = clickHistory(t, m, 4)
	if got := m.selectedIndexes(); len(got) != 2 || got[0] != 2 || got[1] != 4 {
		t.Fatalf("selectedIndexes = %v, want [2 4]", got)
	}
	if !strings.Contains(plainView(m), "選択中 2 件の新しい prefix>") {
		t.Errorf("入力欄が付け替えモードになっていない:\n%s", plainView(m))
	}

	// 選んだ行だけにマーカーが付く（範囲ではない）
	lines := strings.Split(plainView(m), "\n")
	for i := 0; i < 5; i++ {
		marked := strings.Contains(lines[historyTop+i], "▸")
		if want := i == 2 || i == 4; marked != want {
			t.Errorf("履歴 %d 行目のマーカー = %v, want %v (%q)", i, marked, want, lines[historyTop+i])
		}
	}

	// もう一度押すとその行だけ外れる
	m = clickHistory(t, m, 2)
	if got := m.selectedIndexes(); len(got) != 1 || got[0] != 4 {
		t.Errorf("selectedIndexes = %v, want [4]", got)
	}
}

func TestEscClearsSelection(t *testing.T) {
	m := withHistory(t, 3)
	m = clickHistory(t, m, 1)
	m = clickHistory(t, m, 2)
	m, _ = update(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.selectedCount() != 0 || m.message != "" {
		t.Errorf("selectedCount=%d message=%q, want 0 / 空", m.selectedCount(), m.message)
	}
	if !strings.Contains(plainView(m), "新しい prefix>") || strings.Contains(plainView(m), "選択中") {
		t.Errorf("入力欄が通常に戻っていない:\n%s", plainView(m))
	}
}

// 履歴を選んで新しい prefix を入れると実ファイルが付け替わる。
// 最新の1件を含む選択なので、これからの撮影もその prefix になる。
func TestRetagFlowRenamesFilesAndSwitchesPrefix(t *testing.T) {
	m, dir := newTestTUI(t)
	m.w.SetPrefix("01")
	for _, name := range []string{"a.png", "b.png", "c.png"} {
		createFile(t, dir, name)
	}
	m, _ = update(t, m, pollCmd(m.w)().(pollMsg))

	// 新しい順に c, b, a。c と b を選んで 02 にする
	m = clickHistory(t, m, 0)
	m = clickHistory(t, m, 1)
	m = enter(t, typeText(t, m, "02"))

	for _, want := range []string{"01_01_a.png", "02_01_b.png", "02_02_c.png"} {
		if !fileExists(dir, want) {
			t.Errorf("%s が無い: %v", want, dirNames(t, dir))
		}
	}
	if m.selectedCount() != 0 {
		t.Errorf("selectedCount = %d, want 0（付け替え後は解除）", m.selectedCount())
	}
	view := plainView(m)
	for _, want := range []string{"02_02_c.png", "02_01_b.png", "01_01_a.png"} {
		if !strings.Contains(view, want) {
			t.Errorf("履歴に %s が出ていない:\n%s", want, view)
		}
	}
	prefix, next := m.w.Status()
	if prefix != "02" || next != 3 {
		t.Errorf("prefix=%q next=%d, want 02 / 3", prefix, next)
	}
	if m.isErr || !strings.Contains(m.message, "2 件") {
		t.Errorf("message = %q (isErr=%v), want 2件付け替えた通知", m.message, m.isErr)
	}

	// 続けて撮ったファイルが 02_03 になる
	createFile(t, dir, "d.png")
	m, _ = update(t, m, pollCmd(m.w)().(pollMsg))
	if !fileExists(dir, "02_03_d.png") {
		t.Errorf("02_03_d.png が無い: %v", dirNames(t, dir))
	}
}

// 途中だけを付け替えた場合、撮影中の prefix は動かさない。
func TestRetagMiddleKeepsCurrentPrefix(t *testing.T) {
	m, dir := newTestTUI(t)
	m.w.SetPrefix("01")
	for _, name := range []string{"a.png", "b.png", "c.png"} {
		createFile(t, dir, name)
	}
	m, _ = update(t, m, pollCmd(m.w)().(pollMsg))

	// 真ん中の b（新しい順で index 1）だけを 02 にする
	m = clickHistory(t, m, 1)
	m = enter(t, typeText(t, m, "02"))

	if !fileExists(dir, "02_01_b.png") {
		t.Errorf("02_01_b.png が無い: %v", dirNames(t, dir))
	}
	prefix, next := m.w.Status()
	if prefix != "01" || next != 4 {
		t.Errorf("prefix=%q next=%d, want 01 / 4（撮影中の prefix は据え置き）", prefix, next)
	}
	if !strings.Contains(m.message, "1 件") {
		t.Errorf("message = %q, want 付け替えた件数の通知", m.message)
	}
}

// 変え忘れが2回あった場合を、選択2回で直せる。
func TestRetagTwoForgottenBatches(t *testing.T) {
	m, dir := newTestTUI(t)
	m.w.SetPrefix("01")
	// 01 のつもりの2枚、02 のはずの2枚、03 のはずの1枚が全部 01 で撮れてしまった
	for _, name := range []string{"a.png", "b.png", "c.png", "d.png", "e.png"} {
		createFile(t, dir, name)
	}
	m, _ = update(t, m, pollCmd(m.w)().(pollMsg))

	// 新しい順に e, d, c, b, a。まず c と d（index 2,1）を 02 へ
	m = clickHistory(t, m, 1)
	m = clickHistory(t, m, 2)
	m = enter(t, typeText(t, m, "02"))
	// 次に e（index 0）を 03 へ
	m = clickHistory(t, m, 0)
	m = enter(t, typeText(t, m, "03"))

	for _, want := range []string{"01_01_a.png", "01_02_b.png", "02_01_c.png", "02_02_d.png", "03_01_e.png"} {
		if !fileExists(dir, want) {
			t.Errorf("%s が無い: %v", want, dirNames(t, dir))
		}
	}
	// 最後の付け替えは最新を含むので、撮影中の prefix は 03 の続き
	prefix, next := m.w.Status()
	if prefix != "03" || next != 2 {
		t.Errorf("prefix=%q next=%d, want 03 / 2", prefix, next)
	}
	createFile(t, dir, "f.png")
	m, _ = update(t, m, pollCmd(m.w)().(pollMsg))
	if !fileExists(dir, "03_02_f.png") {
		t.Errorf("03_02_f.png が無い: %v", dirNames(t, dir))
	}
}

func TestRetagRejectsInvalidPrefix(t *testing.T) {
	m := withHistory(t, 3)
	m = clickHistory(t, m, 1)
	m = enter(t, typeText(t, m, "a/b"))
	if m.selectedCount() != 1 {
		t.Errorf("selectedCount = %d, want 1（不正な入力で選択は解除しない）", m.selectedCount())
	}
	if !m.isErr {
		t.Errorf("message = %q, want エラー", m.message)
	}
}

// 選択中でも :seq などのコマンドはそのまま使える。
func TestCommandsWorkWhileSelected(t *testing.T) {
	m := withHistory(t, 3)
	m = clickHistory(t, m, 1)
	m = enter(t, typeText(t, m, ":seq 7"))
	if _, next := m.w.Status(); next != 7 {
		t.Errorf("next seq = %d, want 7", next)
	}
	if m.selectedCount() != 1 {
		t.Errorf("selectedCount = %d, want 1（コマンドでは選択を解除しない）", m.selectedCount())
	}
}

// 選択中でも画面の行数は変わらない。
func TestViewHeightWithSelection(t *testing.T) {
	m := withHistory(t, 5)
	want := len(strings.Split(m.View(), "\n"))
	m = clickHistory(t, m, 3)
	m = typeText(t, m, "02")
	if got := len(strings.Split(m.View(), "\n")); got != want {
		t.Errorf("選択中の View は %d 行、want %d 行", got, want)
	}
}

// 選択中に新しいファイルが来ても、選んだ行はそのまま選ばれ続ける
// （選択状態を履歴の行そのものが持っているため、位置がずれない）。
func TestSelectionFollowsRowsOnNewArrivals(t *testing.T) {
	m := withHistory(t, 3) // 新しい順に a2, a1, a0
	m = clickHistory(t, m, 1)
	picked := m.history[1].Rename

	m, _ = update(t, m, pollMsg{results: []Rename{
		{Old: "b0.png", New: "01_04_b0.png"},
		{Old: "b1.png", New: "01_05_b1.png"},
	}})

	idx := m.selectedIndexes()
	if len(idx) != 1 {
		t.Fatalf("selectedIndexes = %v, want 1件（新着は選択に入らない）", idx)
	}
	if got := m.history[idx[0]].Rename; got != picked {
		t.Errorf("選択中の行 = %+v, want %+v", got, picked)
	}
	if m.history[0].New != "01_05_b1.png" || m.history[0].sel {
		t.Errorf("history[0] = %+v, want 新着で未選択", m.history[0])
	}
}

// 選択した行が履歴の上限からあふれても壊れない。
func TestSelectionDroppedWhenHistoryIsCapped(t *testing.T) {
	m := withHistory(t, 3)
	m = clickHistory(t, m, 2)

	results := make([]Rename, historyMax)
	for i := range results {
		results[i] = Rename{Old: fmt.Sprintf("b%d.png", i), New: fmt.Sprintf("01_%d_b%d.png", i, i)}
	}
	m, _ = update(t, m, pollMsg{results: results})

	if m.selectedCount() != 0 {
		t.Errorf("selectedCount = %d, want 0（あふれた行の選択は消える）", m.selectedCount())
	}
	if got := len(strings.Split(m.View(), "\n")); got != m.height {
		t.Errorf("View は %d 行、want %d 行", got, m.height)
	}
}

// うまくいった操作ではメッセージを出さない。
// prefix・連番・停止中かどうかはヘッダに、選択件数は入力欄の見出しに出るので、
// 同じことをメッセージ行で繰り返さない。
func TestSuccessfulActionsStayQuiet(t *testing.T) {
	base, _ := newTestTUI(t)
	base.w.SetPrefix("01")

	for _, tc := range []struct {
		name string
		do   func(m tuiModel) tuiModel
	}{
		{"prefix 切替", func(m tuiModel) tuiModel { return enter(t, typeText(t, m, "case01")) }},
		{"番号送り [ + ]", func(m tuiModel) tuiModel { return clickButton(t, m, 1) }},
		{":seq", func(m tuiModel) tuiModel { return enter(t, typeText(t, m, ":seq 5")) }},
		{"一時停止", func(m tuiModel) tuiModel {
			m, _ = update(t, m, tea.KeyMsg{Type: tea.KeyCtrlP})
			return m
		}},
		{"空入力", func(m tuiModel) tuiModel { return enter(t, typeText(t, m, "   ")) }},
	} {
		if m := tc.do(base); m.message != "" {
			t.Errorf("%s: message = %q, want 空", tc.name, m.message)
		}
	}

	// 履歴の選択でもメッセージは出さない（入力欄の見出しが件数を出す）
	m := withHistory(t, 3)
	m = clickHistory(t, m, 1)
	if m.message != "" {
		t.Errorf("履歴の選択: message = %q, want 空", m.message)
	}
	if !strings.Contains(plainView(m), "選択中 1 件の新しい prefix>") {
		t.Errorf("入力欄に選択件数が出ていない:\n%s", plainView(m))
	}
}

// 画面に出るメッセージに Go のエラー文字列や内部用語を混ぜない。
func TestMessagesAreUserFacing(t *testing.T) {
	for _, tc := range []struct {
		name string
		do   func() tuiModel
	}{
		{"使えない prefix", func() tuiModel {
			m, _ := newTestTUI(t)
			return enter(t, typeText(t, m, "a/b"))
		}},
		{"下限を超えた番号送り", func() tuiModel {
			m, _ := newTestTUI(t)
			m.w.SetPrefix("01")
			return clickButton(t, m, 0)
		}},
		{"数字が無い prefix の番号送り", func() tuiModel {
			m, _ := newTestTUI(t)
			m.w.SetPrefix("case")
			return clickButton(t, m, 0)
		}},
		{"コマンドの指定間違い", func() tuiModel {
			m, _ := newTestTUI(t)
			return enter(t, typeText(t, m, ":seq x"))
		}},
		{"不明なコマンド", func() tuiModel {
			m, _ := newTestTUI(t)
			return enter(t, typeText(t, m, ":bogus"))
		}},
	} {
		m := tc.do()
		if m.message == "" {
			t.Errorf("%s: メッセージが空、want エラー通知", tc.name)
			continue
		}
		for _, bad := range []string{"usage", "must not", "invalid", "error", "Error", "%!"} {
			if strings.Contains(m.message, bad) {
				t.Errorf("%s: message = %q に内部用語 %q が入っている", tc.name, m.message, bad)
			}
		}
	}
}
