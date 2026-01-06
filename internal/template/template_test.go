package template

import (
	"testing"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name        string
		template    string
		wantPlaceholders []string
	}{
		{
			name:        "no placeholders",
			template:    "static string",
			wantPlaceholders: []string{},
		},
		{
			name:        "single placeholder",
			template:    "${file}",
			wantPlaceholders: []string{"file"},
		},
		{
			name:        "multiple placeholders",
			template:    "${path}/${file}",
			wantPlaceholders: []string{"path", "file"},
		},
		{
			name:        "duplicate placeholders",
			template:    "${file} ${file}",
			wantPlaceholders: []string{"file"},
		},
		{
			name:        "mixed content",
			template:    "copy ${source} to ${dest}",
			wantPlaceholders: []string{"source", "dest"},
		},
		{
			name:        "with underscores and dashes",
			template:    "${file_name} ${dest-path}",
			wantPlaceholders: []string{"file_name", "dest-path"},
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpl, err := Parse(tt.template)
			if err != nil {
				t.Fatalf("Parse() error = %v", err)
			}
			
			got := tmpl.Placeholders()
			if len(got) != len(tt.wantPlaceholders) {
				t.Errorf("got %d placeholders, want %d", len(got), len(tt.wantPlaceholders))
				return
			}
			
			for i, want := range tt.wantPlaceholders {
				if got[i] != want {
					t.Errorf("placeholder[%d] = %v, want %v", i, got[i], want)
				}
			}
		})
	}
}

func TestExpand(t *testing.T) {
	tests := []struct {
		name     string
		template string
		args     map[string]string
		want     string
	}{
		{
			name:     "no placeholders",
			template: "static",
			args:     map[string]string{},
			want:     "static",
		},
		{
			name:     "single placeholder",
			template: "${file}",
			args:     map[string]string{"file": "test.bin"},
			want:     "test.bin",
		},
		{
			name:     "multiple placeholders",
			template: "${path}/${file}",
			args:     map[string]string{"path": "/tmp", "file": "test.bin"},
			want:     "/tmp/test.bin",
		},
		{
			name:     "missing argument",
			template: "${file}",
			args:     map[string]string{},
			want:     "",
		},
		{
			name:     "duplicate placeholders",
			template: "${file} and ${file}",
			args:     map[string]string{"file": "test.bin"},
			want:     "test.bin and test.bin",
		},
		{
			name:     "extra arguments ignored",
			template: "${file}",
			args:     map[string]string{"file": "test.bin", "extra": "ignored"},
			want:     "test.bin",
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpl, err := Parse(tt.template)
			if err != nil {
				t.Fatalf("Parse() error = %v", err)
			}
			
			got := tmpl.Expand(tt.args)
			if got != tt.want {
				t.Errorf("Expand() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestValidateArgNames(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr bool
	}{
		{
			name:    "valid names",
			args:    []string{"file", "path", "file_name", "dest-path"},
			wantErr: false,
		},
		{
			name:    "invalid with space",
			args:    []string{"file name"},
			wantErr: true,
		},
		{
			name:    "invalid with special char",
			args:    []string{"file$name"},
			wantErr: true,
		},
		{
			name:    "invalid with dot",
			args:    []string{"file.name"},
			wantErr: true,
		},
		{
			name:    "empty string",
			args:    []string{""},
			wantErr: true,
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateArgNames(tt.args)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateArgNames() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidatePlaceholders(t *testing.T) {
	tests := []struct {
		name      string
		templates []string
		validArgs []string
		wantErr   bool
	}{
		{
			name:      "all placeholders valid",
			templates: []string{"${file}", "${path}/${file}"},
			validArgs: []string{"file", "path"},
			wantErr:   false,
		},
		{
			name:      "unknown placeholder",
			templates: []string{"${unknown}"},
			validArgs: []string{"file", "path"},
			wantErr:   true,
		},
		{
			name:      "no placeholders",
			templates: []string{"static"},
			validArgs: []string{"file"},
			wantErr:   false,
		},
		{
			name:      "empty valid args",
			templates: []string{"${file}"},
			validArgs: []string{},
			wantErr:   true,
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			templates := make([]*Template, len(tt.templates))
			for i, tmplStr := range tt.templates {
				tmpl, err := Parse(tmplStr)
				if err != nil {
					t.Fatalf("Parse() error = %v", err)
				}
				templates[i] = tmpl
			}
			
			err := ValidatePlaceholders(templates, tt.validArgs)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidatePlaceholders() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestExpandAll(t *testing.T) {
	tests := []struct {
		name      string
		templates []string
		args      map[string]string
		want      []string
	}{
		{
			name:      "multiple templates",
			templates: []string{"${file}", "${path}/${file}"},
			args:      map[string]string{"file": "test.bin", "path": "/tmp"},
			want:      []string{"test.bin", "/tmp/test.bin"},
		},
		{
			name:      "empty templates",
			templates: []string{},
			args:      map[string]string{},
			want:      []string{},
		},
		{
			name:      "static templates",
			templates: []string{"static1", "static2"},
			args:      map[string]string{},
			want:      []string{"static1", "static2"},
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ExpandAll(tt.templates, tt.args)
			if err != nil {
				t.Fatalf("ExpandAll() error = %v", err)
			}
			
			if len(got) != len(tt.want) {
				t.Errorf("got %d results, want %d", len(got), len(tt.want))
				return
			}
			
			for i, want := range tt.want {
				if got[i] != want {
					t.Errorf("result[%d] = %v, want %v", i, got[i], want)
				}
			}
		})
	}
}
