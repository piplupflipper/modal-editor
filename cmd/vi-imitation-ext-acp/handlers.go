package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/piplupflipper/modal-editor/ext"
)

// handleRequest は、エージェントからのリクエストに答える。lineConn が 1 件ごとに別の goroutine で呼ぶので、
// 本体の応答（choice/ask の利用者の選択など）を待ってよい。ctx は、エージェントとの接続が終わると取り消される。
func (c *client) handleRequest(ctx context.Context, method string, params json.RawMessage) (any, error) {
	switch method {
	case methodRequestPermission:
		var p requestPermissionParams
		if err := decode(method, params, &p); err != nil {
			return nil, err
		}
		return c.requestPermission(ctx, &p), nil
	case methodReadTextFile:
		var p readTextFileParams
		if err := decode(method, params, &p); err != nil {
			return nil, err
		}
		return c.readTextFile(ctx, &p)
	case methodWriteTextFile:
		var p writeTextFileParams
		if err := decode(method, params, &p); err != nil {
			return nil, err
		}
		return c.writeTextFile(ctx, &p)
	}
	// 端末（terminal/*）は能力を申し出ていないので、エージェントは送らない（基本設計 6.5）。
	return nil, &rpcError{Code: codeMethodNotFound, Message: "method not found: " + method}
}

func decode(method string, params json.RawMessage, v any) error {
	if len(params) == 0 {
		params = []byte("{}")
	}
	if err := json.Unmarshal(params, v); err != nil {
		return &rpcError{Code: codeInvalidParams, Message: fmt.Sprintf("%s: invalid params: %v", method, err)}
	}
	return nil
}

// handleNotification は、エージェントからの通知を受け取る。lineConn が届いた順に呼ぶ。
func (c *client) handleNotification(method string, params json.RawMessage) {
	if method != methodSessionUpdate {
		return
	}
	var p sessionUpdateParams
	if err := json.Unmarshal(params, &p); err != nil {
		c.logf("session/update: %v", err)
		return
	}
	c.showUpdate(&p.Update)
}

// showUpdate は、session/update の更新を acp の欄へ書き足す（基本設計 6.5）。
// agent_message_chunk の本文はそのまま書き足し、tool_call、tool_call_update、plan は 1 行の要約にする。
// ツールの呼び出しの locations は要約の行に書くだけで、カーソルは動かさない。
// それ以外の更新の種類（利用者の発言の再生、思考の過程など）は表示しない。
//
// req: FR-34
func (c *client) showUpdate(u *sessionUpdate) {
	switch u.SessionUpdate {
	case updateAgentMessageChunk:
		var b contentBlock
		if err := json.Unmarshal(u.Content, &b); err != nil {
			c.logf("agent_message_chunk: %v", err)
			return
		}
		if b.Type == "text" {
			c.appendText(b.Text)
			return
		}
		c.appendLine("[" + b.Type + "]")
	case updateToolCall:
		c.rememberToolCall(u)
		c.appendLine("[tool] " + toolSummary(u.Title, u.Kind, u.Status, u.Locations))
	case updateToolCallUpdate:
		c.rememberToolCall(u)
		c.appendLine("[tool " + u.ToolCallID + "] " + toolSummary(u.Title, u.Kind, u.Status, u.Locations))
	case updatePlan:
		parts := make([]string, 0, len(u.Entries))
		for _, e := range u.Entries {
			parts = append(parts, e.Content+" ("+e.Status+")")
		}
		c.appendLine("[plan] " + strings.Join(parts, "; "))
	}
}

// rememberToolCall は、tool_call と tool_call_update で届いた表示の項目（title、kind、locations）を、
// toolCallId ごとに覚える。tool_call_update は変わった項目だけを持つので、空でない項目だけを上書きする。
func (c *client) rememberToolCall(u *sessionUpdate) {
	if u.ToolCallID == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.toolCalls[u.ToolCallID] = mergeToolCall(c.toolCalls[u.ToolCallID], toolCallUpdate{
		ToolCallID: u.ToolCallID, Title: u.Title, Kind: u.Kind, Locations: u.Locations,
	})
}

// mergeToolCall は、base に、next の空でない項目を上書きしたものを返す。
func mergeToolCall(base, next toolCallUpdate) toolCallUpdate {
	base.ToolCallID = next.ToolCallID
	if next.Title != "" {
		base.Title = next.Title
	}
	if next.Kind != "" {
		base.Kind = next.Kind
	}
	if len(next.Locations) > 0 {
		base.Locations = next.Locations
	}
	return base
}

// toolSummary は、ツールの呼び出しの 1 行の要約を作る。空の項目は省く。
// 場所は「パス:行」の形で、行は ACP と同じく 1 から数える。
func toolSummary(title, kind, status string, locs []toolCallLocation) string {
	var sb strings.Builder
	sb.WriteString(title)
	var attrs []string
	for _, a := range []string{kind, status} {
		if a != "" {
			attrs = append(attrs, a)
		}
	}
	if len(attrs) > 0 {
		if sb.Len() > 0 {
			sb.WriteByte(' ')
		}
		sb.WriteString("(" + strings.Join(attrs, ", ") + ")")
	}
	for _, l := range locs {
		sb.WriteByte(' ')
		sb.WriteString(l.Path)
		if l.Line != nil {
			fmt.Fprintf(&sb, ":%d", *l.Line)
		}
	}
	return strings.TrimSpace(sb.String())
}

// requestPermission は、session/request_permission を本体の choice/ask に写し、利用者の選択を返す（基本設計 6.5）。
// toolCall の内容から問いの文を、options から選択肢（ID は optionId、表示は name）を作る。
// toolCall は toolCallId だけのこともある（ACP の Tool Calls の例）ので、同じプロンプトの tool_call と tool_call_update で
// 届いた項目で、欠けた title、kind、locations を補う。それでも何も無ければ toolCallId を示す。
//
// 利用者が取り消したとき、プロンプトが取り消されたとき（acp.cancel）、プロンプトの外で届いたとき、
// 本体へ問えなかったときは、cancelled を返す（ACP の Tool Calls、Prompt Turn）。
//
// req: FR-34
func (c *client) requestPermission(ctx context.Context, p *requestPermissionParams) requestPermissionResult {
	cancelled := requestPermissionResult{Outcome: permissionOutcome{Outcome: "cancelled"}}
	c.mu.Lock()
	turn := c.turn
	tc := mergeToolCall(c.toolCalls[p.ToolCall.ToolCallID], p.ToolCall)
	c.mu.Unlock()
	if turn == nil {
		return cancelled
	}
	// 接続が終わったときと、プロンプトが取り消されたときの両方で問いを取り下げる。
	ask, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(turn, cancel)
	defer stop()

	choices := make([]ext.Choice, 0, len(p.Options))
	for _, o := range p.Options {
		choices = append(choices, ext.Choice{ID: o.OptionID, Label: o.Name})
	}
	what := toolSummary(tc.Title, tc.Kind, "", tc.Locations)
	if what == "" {
		what = "ツールの呼び出し " + tc.ToolCallID
	}
	title := "エージェントが権限を求めている: " + what
	res, err := c.srv.AskChoice(ask, ext.ChoiceAskParams{Title: title, Choices: choices})
	if err != nil {
		if ask.Err() == nil {
			c.logf("choice/ask: %v", err)
		}
		return cancelled
	}
	if res.Cancelled || res.Selected == "" || turn.Err() != nil {
		return cancelled
	}
	for _, o := range p.Options {
		if o.OptionID == res.Selected {
			return requestPermissionResult{Outcome: permissionOutcome{Outcome: "selected", OptionID: o.OptionID}}
		}
	}
	c.logf("choice/ask: unknown choice %q", res.Selected)
	return cancelled
}
