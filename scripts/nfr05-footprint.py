#!/usr/bin/env python3
"""端末で動くエディタの、起動から最初の画面までの時間と RSS を測る（NFR-05、#120）。

エディタを偽の端末（pty）の中で起動し、出力を読み続ける。
出力が --settle 秒途切れた時点で、最初の画面を描き終えたとみなす。時刻は起動（fork の直前）から数え、次の 3 つを出す。

  first_byte_ms  最初の出力を読んだ時刻
  first_text_ms  エスケープシーケンスを除いて文字を含む最初の read の時刻
  text_ms       途切れるまでの出力のうち、エスケープシーケンスを除いて文字を含む最後の read の時刻（最初の画面までの時間）
  settle_ms      途切れるまでの出力の最後の read の時刻（カーソルの表示などの制御だけの出力を含む）

その後 --wait 秒待ってから /proc/<pid>/status の VmRSS と VmHWM を読み、子孫のプロセス（拡張機能など）の VmRSS も読む。
読み終えたら <Esc>:qa!<CR> を送って終わらせる。

使い方は docs/measurements/nfr05-footprint.md にある。Python 3 の標準ライブラリだけを使い、Linux でだけ動く。

    scripts/nfr05-footprint.py --runs 20 --label me-empty -- bin/modal-editor empty.txt

エディタの環境変数の XDG_CONFIG_HOME、XDG_DATA_HOME、XDG_STATE_HOME、XDG_CACHE_HOME は、
1 回ごとに作る空の一時ディレクトリにする（利用者の設定と拡張機能を読まないため）。
--config を渡すと、その内容を $XDG_CONFIG_HOME/modal-editor/config.jsonc に置く。
"""

import argparse
import fcntl
import os
import re
import select
import shutil
import signal
import statistics
import struct
import subprocess
import sys
import tempfile
import termios
import time


def status_kib(pid, key):
    """/proc/<pid>/status の key（VmRSS など）の値を KiB で返す。読めなければ None。"""
    try:
        with open(f"/proc/{pid}/status") as f:
            for line in f:
                if line.startswith(key + ":"):
                    return int(line.split()[1])
    except OSError:
        return None
    return None


def descendants(pid):
    """pid の子孫のプロセスの (pid, 名前) を返す。"""
    children = {}
    for d in os.listdir("/proc"):
        if not d.isdigit():
            continue
        try:
            with open(f"/proc/{d}/stat") as f:
                s = f.read()
        except OSError:
            continue
        # comm は括弧に囲まれ、空白を含みうるので、最後の ')' の後ろを読む。
        # comm は 15 バイトで切れる（modal-editor-ext-lsp と modal-editor-ext-treesitter が同じになる）ので、名前は cmdline から取る。
        name = s[s.index("(") + 1 : s.rindex(")")]
        ppid = int(s[s.rindex(")") + 2 :].split()[1])
        try:
            with open(f"/proc/{d}/cmdline", "rb") as f:
                arg0 = f.read().split(b"\0")[0].decode(errors="replace")
            if arg0:
                name = os.path.basename(arg0)
        except OSError:
            pass
        children.setdefault(ppid, []).append((int(d), name))
    out = []
    stack = [pid]
    while stack:
        for c in children.get(stack.pop(), []):
            out.append(c)
            stack.append(c[0])
    return out


# ANSWERS は、端末への問い合わせと、それに返す応答である。
# Neovim は起動のときに問い合わせを送り、応答を待つ（応答が無いと、約 100 ms 待ってから最初の画面を描く）。
# 実際の端末は応答を返すので、計測でも、Neovim が待つ問い合わせにだけ xterm と同じ形の応答を返す。
# 1 回の read をまたいで分かれた問い合わせには応答しない。
ANSWERS = [
    (b"\x1b[c", b"\x1b[?62;22c"),  # 1 次の装置属性（DA1）
    (b"\x1b[0c", b"\x1b[?62;22c"),
    (b"\x1b[5n", b"\x1b[0n"),  # 装置の状態（DSR）
    (b"\x1b]10;?\x07", b"\x1b]10;rgb:ffff/ffff/ffff\x07"),  # 文字の色
    (b"\x1b]11;?\x07", b"\x1b]11;rgb:0000/0000/0000\x07"),  # 背景の色
    (b"\x1bP+q4D73\x1b\\", b"\x1bP0+r\x1b\\"),  # terminfo の Ms の問い合わせ（XTGETTCAP）。持たないと答える
]


ESCAPES = re.compile(
    rb"\x1b\[[0-?]*[ -/]*[@-~]"  # CSI
    rb"|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)"  # OSC
    rb"|\x1b[P_^][^\x1b]*\x1b\\"  # DCS、APC、PM
    rb"|\x1b[ -/]*[0-~]"  # その他の 2 バイトの列（ESC = など）
)


def has_text(data):
    """data がエスケープシーケンスを除いて、空白でない文字を含むかを返す。"""
    return any(b > 0x20 and b != 0x7F for b in ESCAPES.sub(b"", data))


def answer(master, data):
    """data に含まれる問い合わせに、出現の順に応答を書く。"""
    found = []
    for q, a in ANSWERS:
        start = 0
        while (i := data.find(q, start)) >= 0:
            found.append((i, a))
            start = i + len(q)
    for _, a in sorted(found):
        try:
            os.write(master, a)
        except OSError:
            return


def run_once(argv, args, dump=None):
    """argv を 1 回起動して測る。dump がリストなら、最初の画面までの出力を、読んだ時刻と一緒に書き足す。"""
    tmp = tempfile.mkdtemp(prefix="nfr05-")
    try:
        env = dict(os.environ)
        env["TERM"] = args.term
        for name in ("CONFIG", "DATA", "STATE", "CACHE"):
            d = os.path.join(tmp, name.lower())
            os.makedirs(d)
            env[f"XDG_{name}_HOME"] = d
        if args.config:
            d = os.path.join(tmp, "config", "modal-editor")
            os.makedirs(d)
            shutil.copy(args.config, os.path.join(d, "config.jsonc"))
        for kv in args.env:
            k, v = kv.split("=", 1)
            env[k] = v

        master, slave = os.openpty()
        fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", args.rows, args.cols, 0, 0))

        def setup():
            os.setsid()
            fcntl.ioctl(0, termios.TIOCSCTTY, 0)

        t0 = time.perf_counter()
        proc = subprocess.Popen(
            argv, stdin=slave, stdout=slave, stderr=slave, env=env, preexec_fn=setup, close_fds=True
        )
        os.close(slave)

        first = last = text = first_text = None
        nbytes = 0
        deadline = t0 + args.timeout
        while True:
            now = time.perf_counter()
            if last is not None and now - last >= args.settle:
                break
            if now >= deadline:
                break
            wait = args.settle if last is None else args.settle - (now - last)
            r, _, _ = select.select([master], [], [], min(wait, deadline - now))
            if not r:
                continue
            try:
                data = os.read(master, 65536)
            except OSError:
                break
            if not data:
                break
            t = time.perf_counter()
            if first is None:
                first = t
            last = t
            if has_text(data):
                if first_text is None:
                    first_text = t
                text = t
            nbytes += len(data)
            if dump is not None:
                dump.append(f"{(t - t0) * 1000:9.1f} ms {data!r}\n")
            if args.answer:
                answer(master, data)

        # 待つ間も読み続ける（読まないと、エディタが書き込みで止まる）。
        end = time.perf_counter() + args.wait
        while True:
            left = end - time.perf_counter()
            if left <= 0:
                break
            r, _, _ = select.select([master], [], [], left)
            if r:
                try:
                    os.read(master, 65536)
                except OSError:
                    break

        res = {
            "first_byte_ms": None if first is None else (first - t0) * 1000,
            "first_text_ms": None if first_text is None else (first_text - t0) * 1000,
            "text_ms": None if text is None else (text - t0) * 1000,
            "settle_ms": None if last is None else (last - t0) * 1000,
            "bytes": nbytes,
            "rss_kib": status_kib(proc.pid, "VmRSS"),
            "hwm_kib": status_kib(proc.pid, "VmHWM"),
            "children": [(name, status_kib(p, "VmRSS")) for p, name in descendants(proc.pid)],
        }

        # modal-editor は :qa を持たないので、ファイルを 1 つだけ開いて :q! で終わらせる。
        try:
            os.write(master, b"\x1b:q!\r")
        except OSError:
            pass
        end = time.perf_counter() + args.quit_timeout
        while proc.poll() is None and time.perf_counter() < end:
            r, _, _ = select.select([master], [], [], 0.05)
            if r:
                try:
                    os.read(master, 65536)
                except OSError:
                    pass
        if proc.poll() is None:
            res["killed"] = True
            os.killpg(proc.pid, signal.SIGKILL)
            proc.wait()
        os.close(master)
        return res
    finally:
        shutil.rmtree(tmp, ignore_errors=True)


def summary(values):
    v = sorted(x for x in values if x is not None)
    if not v:
        return "n=0"
    return f"min={v[0]:.1f} p50={statistics.median(v):.1f} max={v[-1]:.1f} n={len(v)}"


def main():
    p = argparse.ArgumentParser(description=__doc__.split("\n")[0])
    p.add_argument("--runs", type=int, default=20, help="起動する回数")
    p.add_argument("--settle", type=float, default=0.5, help="出力がこの秒数途切れたら、最初の画面を描き終えたとみなす")
    p.add_argument("--wait", type=float, default=0.0, help="最初の画面の後、RSS を読むまでに待つ秒数")
    p.add_argument("--timeout", type=float, default=30.0, help="最初の画面を待つ上限の秒数")
    p.add_argument("--quit-timeout", type=float, default=10.0, help="終了を待つ上限の秒数。超えたら SIGKILL で終わらせる")
    p.add_argument("--rows", type=int, default=24)
    p.add_argument("--cols", type=int, default=80)
    p.add_argument("--term", default="xterm-256color", help="エディタに渡す TERM")
    p.add_argument("--config", help="modal-editor の設定ファイルとして置くファイル")
    p.add_argument("--env", action="append", default=[], help="エディタに足す環境変数（K=V）")
    p.add_argument("--label", default="", help="出力の各行の先頭に付ける名前")
    p.add_argument("--no-answer", dest="answer", action="store_false", help="端末への問い合わせに応答しない")
    p.add_argument("--dump", help="最後の回の、最初の画面までの出力を、読んだ時刻と一緒に書くファイル")
    p.add_argument("argv", nargs="+", help="エディタのコマンドと引数（-- の後に書く）")
    args = p.parse_args()

    results = []
    for i in range(args.runs):
        dump = [] if args.dump and i == args.runs - 1 else None
        r = run_once(args.argv, args, dump)
        if dump is not None:
            with open(args.dump, "w") as f:
                f.writelines(dump)
        results.append(r)
        kids = " ".join(f"{n}={k}" for n, k in r["children"])
        print(
            f"{args.label} run={i + 1} first_byte_ms={r['first_byte_ms'] or 0:.1f} "
            f"first_text_ms={r['first_text_ms'] or 0:.1f} text_ms={r['text_ms'] or 0:.1f} "
            f"settle_ms={r['settle_ms'] or 0:.1f} "
            f"bytes={r['bytes']} rss_kib={r['rss_kib']} hwm_kib={r['hwm_kib']}"
            + (f" children: {kids}" if kids else "")
            + (" killed" if r.get("killed") else ""),
            flush=True,
        )
    print(f"{args.label} summary first_text_ms {summary(r['first_text_ms'] for r in results)}")
    print(f"{args.label} summary text_ms {summary(r['text_ms'] for r in results)}")
    print(f"{args.label} summary settle_ms {summary(r['settle_ms'] for r in results)}")
    print(f"{args.label} summary first_byte_ms {summary(r['first_byte_ms'] for r in results)}")
    print(f"{args.label} summary rss_kib {summary(r['rss_kib'] for r in results)}")
    print(f"{args.label} summary hwm_kib {summary(r['hwm_kib'] for r in results)}")
    names = sorted({n for r in results for n, _ in r["children"]})
    for n in names:
        print(f"{args.label} summary child {n} rss_kib {summary(k for r in results for m, k in r['children'] if m == n)}")
    print(f"{args.label} summary children_total_kib {summary(sum(k or 0 for _, k in r['children']) for r in results)}")


if __name__ == "__main__":
    sys.exit(main())
