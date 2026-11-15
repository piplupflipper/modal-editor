package ext

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// validManifest は、基本設計 5.1 の例と同じ内容の宣言ファイルを返す。
func validManifest() Manifest {
	return Manifest{
		ID:      "surround",
		Command: "./modal-editor-ext-surround",
		Activation: []Activation{
			{On: ActivationOperator, ID: "surround.add"},
			{On: ActivationFileType, Pattern: "markdown"},
		},
		Contributes: Contributes{
			Operators:   []Contribution{{ID: "surround.add", Keys: "ys"}},
			TextObjects: []Contribution{{ID: "surround.inner", Keys: "is"}},
		},
	}
}

// req: FR-13, FR-14
func TestManifestJSON(t *testing.T) {
	// 基本設計 5.1 の例からコメントを除いたもの。
	const doc = `{
  "id": "surround",
  "command": "./modal-editor-ext-surround",
  "activation": [
    { "on": "operator", "id": "surround.add" },
    { "on": "filetype", "pattern": "markdown" }
  ],
  "contributes": {
    "operators": [{ "id": "surround.add", "keys": "ys" }],
    "textObjects": [{ "id": "surround.inner", "keys": "is" }]
  }
}`
	var got Manifest
	if err := json.Unmarshal([]byte(doc), &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if want := validManifest(); !reflect.DeepEqual(got, want) {
		t.Errorf("Unmarshal = %#v, want %#v", got, want)
	}
	if err := got.Validate(); err != nil {
		t.Errorf("Validate = %v, want nil", err)
	}
	roundTrip(t, got, `{"id":"surround","command":"./modal-editor-ext-surround",`+
		`"activation":[{"on":"operator","id":"surround.add"},{"on":"filetype","pattern":"markdown"}],`+
		`"contributes":{"operators":[{"id":"surround.add","keys":"ys"}],"textObjects":[{"id":"surround.inner","keys":"is"}]}}`)
}

// req: FR-13, FR-14
func TestManifestValidate(t *testing.T) {
	tests := []struct {
		name   string
		modify func(m *Manifest)
		// want は、誤りのメッセージに含まれるべき文字列の一覧である。空なら誤りが無いことを期待する。
		want []string
	}{
		{name: "正しい", modify: func(*Manifest) {}},
		{
			name: "全種類の操作の契機",
			modify: func(m *Manifest) {
				m.Contributes.Motions = []Contribution{{ID: "m"}}
				m.Contributes.Commands = []Contribution{{ID: "c"}}
				m.Activation = []Activation{
					{On: ActivationOperator, ID: "surround.add"},
					{On: ActivationMotion, ID: "m"},
					{On: ActivationTextObject, ID: "surround.inner"},
					{On: ActivationCommand, ID: "c"},
				}
			},
		},
		{name: "id が空", modify: func(m *Manifest) { m.ID = "" }, want: []string{"id が空"}},
		{name: "id が空白を含む", modify: func(m *Manifest) { m.ID = "sur round" }, want: []string{`id "sur round" が空白を含む`}},
		{name: "command が空", modify: func(m *Manifest) { m.Command = "" }, want: []string{"command が空"}},
		{name: "activation が空", modify: func(m *Manifest) { m.Activation = nil }, want: []string{"activation が空"}},
		{
			name:   "未知の契機",
			modify: func(m *Manifest) { m.Activation = []Activation{{On: "startup"}} },
			want:   []string{`activation[0].on "startup" は未知の契機`},
		},
		{
			name:   "契機の id が空",
			modify: func(m *Manifest) { m.Activation[0].ID = "" },
			want:   []string{"activation[0].id が空"},
		},
		{
			name:   "契機の id が contributes に無い",
			modify: func(m *Manifest) { m.Activation[0].ID = "surround.del" },
			want:   []string{`activation[0].id "surround.del" が contributes の operator に無い`},
		},
		{
			name:   "契機の id が別の種類の操作を指す",
			modify: func(m *Manifest) { m.Activation[0].ID = "surround.inner" },
			want:   []string{`activation[0].id "surround.inner" が contributes の operator に無い`},
		},
		{
			name:   "filetype の pattern が空",
			modify: func(m *Manifest) { m.Activation[1].Pattern = "" },
			want:   []string{"activation[1].pattern が空"},
		},
		{
			name:   "操作の id が空",
			modify: func(m *Manifest) { m.Contributes.Motions = []Contribution{{Keys: "x"}} },
			want:   []string{"contributes.motions[0].id が空"},
		},
		{
			name: "操作の id が種類をまたいで重複",
			modify: func(m *Manifest) {
				m.Contributes.Commands = []Contribution{{ID: "surround.add"}}
			},
			want: []string{`contributes.commands[0].id "surround.add" が contributes.operators[0] と重複`},
		},
		{
			name: "操作の id が同じ種類で重複",
			modify: func(m *Manifest) {
				m.Contributes.TextObjects = append(m.Contributes.TextObjects, Contribution{ID: "surround.inner"})
			},
			want: []string{`contributes.textObjects[1].id "surround.inner" が contributes.textObjects[0] と重複`},
		},
		{
			name: "複数の誤りをすべて返す",
			modify: func(m *Manifest) {
				m.ID = ""
				m.Command = ""
				m.Activation[1].Pattern = ""
			},
			want: []string{"id が空", "command が空", "activation[1].pattern が空"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := validManifest()
			tt.modify(&m)
			err := m.Validate()
			if len(tt.want) == 0 {
				if err != nil {
					t.Fatalf("Validate = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Validate = nil, want errors containing %q", tt.want)
			}
			for _, w := range tt.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("Validate = %q, want containing %q", err, w)
				}
			}
			if got, want := strings.Count(err.Error(), "\n")+1, len(tt.want); got != want {
				t.Errorf("Validate の誤りの数 = %d, want %d: %q", got, want, err)
			}
		})
	}
}

// req: FR-13
func TestValidateManifests(t *testing.T) {
	a := validManifest()
	b := validManifest()
	b.ID = "other"
	if err := ValidateManifests([]Manifest{a, b}); err != nil {
		t.Errorf("ValidateManifests = %v, want nil", err)
	}

	dup := validManifest()
	err := ValidateManifests([]Manifest{a, b, dup})
	if err == nil || !strings.Contains(err.Error(), `宣言ファイル[2] の id "surround" が宣言ファイル[0] と重複する`) {
		t.Errorf("ValidateManifests = %v, want duplicate id error", err)
	}

	bad := validManifest()
	bad.Command = ""
	err = ValidateManifests([]Manifest{bad})
	if err == nil || !strings.Contains(err.Error(), `宣言ファイル[0]（id "surround"）: command が空`) {
		t.Errorf("ValidateManifests = %v, want command error", err)
	}

	if err := ValidateManifests(nil); err != nil {
		t.Errorf("ValidateManifests(nil) = %v, want nil", err)
	}
}
