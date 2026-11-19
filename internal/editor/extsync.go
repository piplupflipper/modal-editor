package editor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/piplupflipper/vi-imitation/internal/buffer"
	"github.com/piplupflipper/vi-imitation/internal/config"
	"github.com/piplupflipper/vi-imitation/internal/exthost"
	"github.com/piplupflipper/vi-imitation/internal/extmgr"
	"github.com/piplupflipper/vi-imitation/internal/ui"
)

// :ext（拡張機能の管理。基本設計 5.6、要件定義 4.13）。
//
// コマンドラインの :ext sync と :ext update は、設定ファイルの extensions.install に並べた拡張機能を、
// イベントループの外の goroutine で internal/extmgr の Sync に渡して取得、ビルド、入れ替え、削除する。
// :ext update は、ロックファイルの記録を使わずに版を解決し直す（extmgr.Options.Update。ブランチの版を最新にする）。
//
// :ext は本体のコマンドだが、ex パッケージの表には置かず、拡張機能のコマンド（ex.State.ExtCommand）と同じ口から呼ぶ。
// コマンドラインの表（exCommands）に、拡張機能の ID の代わりに extManagerExtension を入れ、extQuery.Action が受け取る。
// 引数を行末まで取ること、範囲と ! を取らないことは、拡張機能のコマンドと同じである。
//
// 終わった後の扱いは次のとおりにする（仮）。宣言ファイルは読み直さない。
//
//   - 更新したもの（extmgr.ActionUpdated）が本体の使っている拡張機能なら、exthost.Host.Reload で止め、新しい実行ファイルを起動し直す
//     （開いているバッファのファイルの種類で起動するものはすぐに、それ以外は次の起動の契機で）。
//     宣言ファイルの中身が変わったとき（キー、コマンド、起動の契機）は Reload せずに前の版を動かし続け、本体を起動し直すまで反映しない。
//     ID が変わったときは前の ID の拡張機能を止め、本体を起動し直すまで新しい版は使えない
//   - 新しく入れたもの（extmgr.ActionInstalled）は、本体を起動し直すと使える
//   - 削除したもの（extmgr.ActionRemoved）が本体の使っている拡張機能なら、exthost.Host.Disable で止める
//
// 起動し直しが要るものがあれば、結果の知らせにその旨を書く。
// 起動したままの差し替え（exthost と、キーマップとコマンドラインの表を作り直すこと）は、段階 3 の範囲では持たない。

// extManagerExtension は、コマンドラインの表（exCommands）で :ext に入れる拡張機能の ID の代わりの値である。
// 空白を含むので、宣言ファイルの拡張機能の ID（空白を含めない。ext.Manifest.Validate）と重ならない。
const extManagerExtension = " extmgr"

// extCommandName は、:ext のコマンドの名前である。
const extCommandName = "ext"

// ExtManagerConfig は、:ext sync の設定である（基本設計 5.6）。ゼロ値は、本体の既定の置き場所を使う。
type ExtManagerConfig struct {
	// ConfigPath は、extensions.install を読む設定ファイルのパスである。ロックファイルはその隣に置く（FR-103）。
	// 空なら config.DefaultPath を使う。
	ConfigPath string
	// ExtensionDirs は、拡張機能を探すディレクトリ（config.ExtensionDirs の結果）である。
	// 1 つ目に拡張機能を入れ、2 つ目以降を同梱の拡張機能として、隠すかを調べる（extmgr.Options.BundledDirs）。
	// nil なら config.ExtensionDirs に本体の実行ファイルのパスを渡して決める。
	ExtensionDirs []string
	// Git、Go、Env は、extmgr.Options の同じ名前の項目にそのまま渡す。テストが、利用者の Go のキャッシュと git の設定を使わないために入れる。
	Git, Go string
	Env     []string
}

// extSyncJob は、実行中の :ext sync である。イベントループの goroutine だけが触る。
type extSyncJob struct {
	cancel context.CancelFunc
}

// extSyncResult は、:ext sync の goroutine からイベントループへ渡す結果である。
type extSyncResult struct {
	extDir   string
	outcomes []extmgr.Outcome
	err      error
	// configErrs は、設定ファイルを読んだときの誤りである。
	configErrs []error
	// keepUnlisted は、設定の誤りのために削除をしなかったことである。
	keepUnlisted bool
	// before は、Sync の前に extDir にあった拡張機能の名前から宣言ファイルの中身への対応である。
	before map[string][]byte
	// after は、Sync の後の同じ対応である。
	after map[string][]byte
}

// addExtManagerCommand は、コマンドラインの表に :ext を足す。拡張機能が同じ ID のコマンドを宣言していれば、本体の :ext を優先し、誤りを返す。
func addExtManagerCommand(cmds map[string]string) error {
	prev, ok := cmds[extCommandName]
	cmds[extCommandName] = extManagerExtension
	if ok {
		return fmt.Errorf("拡張機能 %s のコマンド %s は本体の :%s と重なるので、コマンドラインからは呼べない", prev, extCommandName, extCommandName)
	}
	return nil
}

// extManagerAction は、コマンドラインの :ext の引数 text を実行する。
// 時間のかかる処理は別の goroutine で始めるだけで、すぐに戻る。
//
// req: FR-102
func (e *Editor) extManagerAction(text string) error {
	switch strings.TrimSpace(text) {
	case "sync":
		return e.startExtSync(false)
	case "update":
		return e.startExtSync(true)
	}
	return fmt.Errorf("E475: 無効な引数です: %s（:ext sync か :ext update）", text)
}

// extManagerTx は、extQuery.Action が :ext に返す、編集の無いトランザクションである。
func extManagerTx(b *buffer.Buffer) buffer.Transaction {
	return buffer.Transaction{BaseVersion: b.Version()}
}

// extPaths は、ExtManagerConfig から、設定ファイルのパスと拡張機能を探すディレクトリを決める。
func (e *Editor) extPaths() (configPath string, dirs []string, err error) {
	mc := e.cfg.ExtManager
	configPath = mc.ConfigPath
	if configPath == "" {
		if configPath, err = config.DefaultPath(); err != nil {
			return "", nil, fmt.Errorf("設定ファイルの場所を決められません: %w", err)
		}
	}
	dirs = mc.ExtensionDirs
	if dirs == nil {
		exe, xerr := os.Executable()
		if xerr != nil {
			exe = ""
		}
		dirs = config.ExtensionDirs(exe)
	}
	if len(dirs) == 0 || dirs[0] == "" {
		return "", nil, errors.New("拡張機能を入れるディレクトリを決められません（XDG_DATA_HOME と HOME が無い）")
	}
	return configPath, dirs, nil
}

// startExtSync は、:ext sync（update が true なら :ext update）を別の goroutine で始める。
// 進み具合はメッセージの行に出し、終わったら extSyncFinished が結果を知らせる。
// Run が戻ったら取り消す（git と go の子プロセスは extmgr がプロセスグループごと終わらせる）。
func (e *Editor) startExtSync(update bool) error {
	if e.extSync != nil {
		return errors.New(":ext sync の実行中です")
	}
	configPath, dirs, err := e.extPaths()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	e.extSync = &extSyncJob{cancel: cancel}
	e.message = infoMessage("拡張機能を取得しています（:ext sync）")
	mc := e.cfg.ExtManager
	stop := e.stop
	post := func(f func()) bool {
		select {
		case e.inspect <- f:
			return true
		case <-stop:
			return false
		}
	}
	go func() {
		defer e.recoverPanic()
		defer cancel()
		res := extSyncResult{extDir: dirs[0]}
		cfg, errs := config.Load(configPath)
		res.configErrs = errs
		res.keepUnlisted = !cfg.ExtensionInstallValid
		specs := make([]extmgr.Spec, len(cfg.ExtensionInstall))
		for i, x := range cfg.ExtensionInstall {
			specs[i] = extmgr.Spec(x)
		}
		res.before = readManifests(dirs[0])
		opts := extmgr.Options{
			ExtensionsDir: dirs[0],
			LockPath:      filepath.Join(filepath.Dir(configPath), extmgr.LockFileName),
			Update:        update,
			KeepUnlisted:  res.keepUnlisted,
			BundledDirs:   dirs[1:],
			Git:           mc.Git,
			Go:            mc.Go,
			Env:           mc.Env,
			Progress: func(ev extmgr.Event) {
				if text := extSyncProgress(ev); text != "" {
					post(func() {
						if e.extSync != nil {
							e.message = infoMessage(text)
						}
					})
				}
			},
		}
		res.outcomes, res.err = extmgr.Sync(ctx, specs, opts)
		res.after = readManifests(dirs[0])
		post(func() { e.extSyncFinished(res) })
	}()
	if stop != nil {
		go func() {
			select {
			case <-stop:
				cancel()
			case <-ctx.Done():
			}
		}()
	}
	return nil
}

// extSyncProgress は、進み具合 ev をメッセージの行の文にする。出さないものは空を返す。
func extSyncProgress(ev extmgr.Event) string {
	switch ev.Phase {
	case extmgr.PhaseFetch, extmgr.PhaseBuild, extmgr.PhaseInstall, extmgr.PhaseRemove:
		return fmt.Sprintf("[%d/%d] %s: %s", ev.Index, ev.Total, ev.Name, ev.Phase)
	case extmgr.PhaseDone:
		return fmt.Sprintf("[%d/%d] %s: %s", ev.Index, ev.Total, ev.Name, ev.Outcome.Action)
	case extmgr.PhaseWarning:
	}
	return ""
}

// readManifests は、dir の直下の拡張機能のディレクトリの名前から、宣言ファイルの中身への対応を返す。読めないものは入れない。
func readManifests(dir string) map[string][]byte {
	out := map[string][]byte{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	for _, de := range entries {
		if data, err := os.ReadFile(filepath.Join(dir, de.Name(), config.ManifestFileName)); err == nil {
			out[de.Name()] = data
		}
	}
	return out
}

// usedSpec は、本体が使っている拡張機能のうち、ID が id で、宣言ファイルのディレクトリが dir のものがあるかを返す。
func (e *Editor) usedSpec(id, dir string) bool {
	if id == "" {
		return false
	}
	for _, s := range e.cfg.Extensions {
		if s.Manifest.ID == id && filepath.Clean(s.Dir) == filepath.Clean(dir) {
			return true
		}
	}
	return false
}

// extSyncFinished は、:ext sync の結果を知らせ、入れ替えた拡張機能と削除した拡張機能を止める。
//
// req: FR-102, FR-103, FR-121, FR-122
func (e *Editor) extSyncFinished(res extSyncResult) {
	e.extSync = nil
	var report []string
	for _, ce := range res.configErrs {
		report = append(report, "設定ファイル: "+ce.Error())
	}
	if res.keepUnlisted {
		report = append(report, "設定ファイルの extensions.install に誤りがあるので、設定から外した拡張機能を削除しなかった")
	}
	counts := map[extmgr.Action]int{}
	restart := false
	for _, o := range res.outcomes {
		counts[o.Action]++
		dir := filepath.Join(res.extDir, o.Name)
		switch o.Action {
		case extmgr.ActionUpdated:
			manifestChanged := !bytes.Equal(res.before[o.Name], res.after[o.Name])
			switch {
			case !e.usedSpec(o.PreviousID, dir):
			case o.ID != o.PreviousID:
				e.stopExtension(o.PreviousID, e.host.Disable)
			case !manifestChanged:
				e.stopExtension(o.PreviousID, e.host.Reload)
			}
			// 宣言ファイルが変わったら（command のパスが変わりうる）、Reload すると前の宣言のまま新しい版を起動して失敗しうるので、
			// 前の版のプロセスを動かし続け、起動し直しを求める（PR #220 のレビューの推奨 3）。
			if o.ID != o.PreviousID || manifestChanged {
				restart = true
				report = append(report, fmt.Sprintf("%s: 宣言ファイルが変わったので、本体を起動し直すと反映する（それまでは前の版が動く）", o.Name))
			}
		case extmgr.ActionInstalled:
			restart = true
			report = append(report, fmt.Sprintf("%s: 入れた。本体を起動し直すと使える", o.Name))
		case extmgr.ActionRemoved:
			if e.usedSpec(o.ID, dir) {
				e.stopExtension(o.ID, e.host.Disable)
			}
		case extmgr.ActionFailed:
			if o.Commit == "" && e.usedSpec(o.PreviousID, dir) {
				// 入れ替えの途中で前の版を戻せず、拡張機能のディレクトリから無くなった。
				e.stopExtension(o.PreviousID, e.host.Disable)
			}
			report = append(report, fmt.Sprintf("%s: 失敗: %v", o.Name, o.Err))
		case extmgr.ActionKept:
			report = append(report, fmt.Sprintf("%s: 手で置いたものなので削除しなかった", o.Name))
		case extmgr.ActionUnchanged:
		}
		if o.Warning != nil {
			report = append(report, fmt.Sprintf("%s: %v", o.Name, o.Warning))
		}
	}

	level := ui.MessageInfo
	var summary string
	switch {
	case res.err != nil && len(res.outcomes) == 0:
		level = ui.MessageError
		summary = "拡張機能の取得に失敗しました: " + res.err.Error()
	default:
		summary = fmt.Sprintf("拡張機能: 追加 %d、更新 %d、削除 %d、変更なし %d、失敗 %d",
			counts[extmgr.ActionInstalled], counts[extmgr.ActionUpdated], counts[extmgr.ActionRemoved],
			counts[extmgr.ActionUnchanged], counts[extmgr.ActionFailed])
		switch {
		case res.err != nil:
			level = ui.MessageError
			report = append(report, "誤り: "+res.err.Error())
			summary += "（誤りあり）"
		case counts[extmgr.ActionFailed] > 0:
			level = ui.MessageError
		case len(report) > 0:
			level = ui.MessageWarning
		}
		if restart {
			summary += "。本体を起動し直すと反映します"
		}
	}
	e.message = ui.Message{Text: summary, Level: level}
	if level == ui.MessageError {
		// 本体の誤りは、:messages で読み直せるように履歴に残す（messages.go。#229 のレビューの推奨 6）。内訳も 1 行ずつ残す。
		e.noteMessage(e.message)
		for _, r := range report {
			e.noteMessage(ui.Message{Text: r, Level: level})
		}
	}
	if len(report) > 0 && e.cur != nil {
		// 1 行に収まらない内訳（失敗の理由、警告、起動し直しの要るもの）は、画面の中央のポップアップに出す。
		// #179 の裁定の ACP と DAP の出力と同じ扱い（オーケストレータの仮の決定）で、端末多重化ソフトの中ではペインに出すが、
		// ペインを開く口がまだ無いので、中でも外の規則（client の画面の中央）で出す。
		// 段階 3 のポップアップの部品（#236）では、中央のポップアップにフォーカスを入れて出し、<Esc> で閉じる。
		e.openTextFloat(":ext sync", summary+"\n\n"+strings.Join(report, "\n"), true)
	}
	e.log.Info("ext sync finished", "summary", summary, "details", strings.Join(report, "; "))
}

// stopExtension は、拡張機能 id を stop（exthost.Host.Reload か Disable）で止める。誤りはログに記録するだけにする。
// 止めた ID を extStopped に記録し、無効になった知らせを異常として出さないようにする。
func (e *Editor) stopExtension(id string, stop func(string) error) {
	if e.extStopped == nil {
		e.extStopped = map[string]bool{}
	}
	e.extStopped[id] = true
	if err := stop(id); err != nil && !errors.Is(err, exthost.ErrClosed) {
		e.log.Warn("拡張機能を止められない", "extension", id, "err", err)
	}
}
