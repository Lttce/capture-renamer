// 設定値の定義。
// 利用者が書き換える唯一のファイルを想定。起動引数やconfigファイルからの読み込みは未実装。
package main

import "time"

const (
	// 監視対象フォルダのパス。デフォルトはカレントディレクトリ。
	SaveFolder = "."
	// 起動時の初期prefix。stdinから変更可能。
	InitialPrefix = "item"
	// ポーリング間隔。新ファイル検出の頻度。
	PollInterval = 300 * time.Millisecond
	// 連番の桁数（例: 2 なら 01, 02, ...）。
	CounterDigits = 2
)
