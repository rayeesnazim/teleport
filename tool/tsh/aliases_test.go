package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func Test_replaceSpecialArgs(t *testing.T) {
	tests := []struct {
		name        string
		argsGiven   []string
		aliasParams []string
		want        []string
		wantErr     bool
	}{
		{
			name:        "empty",
			argsGiven:   nil,
			aliasParams: nil,
			want:        nil,
			wantErr:     false,
		},
		{
			name:        "empty alias no pass",
			argsGiven:   []string{"foo", "bar", "baz"},
			aliasParams: nil,
			want:        nil,
			wantErr:     false,
		},
		{
			name:        "no references, ignore args",
			argsGiven:   []string{"arg1", "arg2", "arg3"},
			aliasParams: []string{"foo1", "foo2", "foo3"},
			want:        []string{"foo1", "foo2", "foo3"},
			wantErr:     false,
		},
		{
			name:        "valid references",
			argsGiven:   []string{"foo", "bar"},
			aliasParams: []string{"refFoo", "$0", "refBar", "$1"},
			want:        []string{"refFoo", "foo", "refBar", "bar"},
			wantErr:     false,
		},
		{
			name:        "out of range reference",
			argsGiven:   []string{"foo"},
			aliasParams: []string{"refFoo", "$0", "refBar", "$1"},
			want:        []string{"refFoo", "foo", "refBar", ""},
			wantErr:     false,
		},
		{
			name:        "all args",
			argsGiven:   []string{"foo", "bar"},
			aliasParams: []string{"$@"},
			want:        []string{"foo", "bar"},
			wantErr:     false,
		},
		{
			name:        "all args and references",
			argsGiven:   []string{"foo", "bar"},
			aliasParams: []string{"$@", "$0"},
			want:        []string{"foo", "bar", "foo"},
			wantErr:     false,
		},
		{
			name:        "ignore negative reference",
			argsGiven:   nil,
			aliasParams: []string{"$-100"},
			want:        []string{"$-100"},
			wantErr:     false,
		},
		{
			name:        "ignore unknown references",
			argsGiven:   nil,
			aliasParams: []string{"$100 200 300"},
			want:        []string{"$100 200 300"},
			wantErr:     false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := replaceSpecialArgs(tt.argsGiven, tt.aliasParams)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.want, got)
			}
		})
	}
}

func Test_findCommand(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		wantAlias string
		wantIndex int
	}{
		{
			name:      "empty args not found",
			args:      nil,
			wantAlias: "",
			wantIndex: -1,
		},
		{
			name:      "only options, not found",
			args:      []string{"--foo", "--bar", "-baz", "--"},
			wantAlias: "",
			wantIndex: -1,
		},
		{
			name:      "first place",
			args:      []string{"login", "--foo", "--bar"},
			wantAlias: "login",
			wantIndex: 0,
		},
		{
			name:      "second place",
			args:      []string{"--foo", "login", "--bar"},
			wantAlias: "login",
			wantIndex: 1,
		},
		{
			name:      "last place",
			args:      []string{"--foo", "--bar", "login"},
			wantAlias: "login",
			wantIndex: 2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, i := findCommand(tt.args)
			require.Equal(t, tt.wantAlias, a)
			require.Equal(t, tt.wantIndex, i)
		})
	}
}

func Test_getSeenAliases(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want []string
	}{
		{
			name: "empty",
			env:  nil,
			want: nil,
		},
		{
			name: "commas",
			env:  map[string]string{tshAliasEnvKey: ",,,"},
			want: nil,
		},
		{
			name: "few values",
			env:  map[string]string{tshAliasEnvKey: "foo,bar,baz,,,"},
			want: []string{"foo", "bar", "baz"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			getEnv := func(key string) string {
				val, _ := tt.env[key]
				return val
			}

			require.Equal(t, tt.want, getSeenAliases(getEnv))
		})
	}
}

func Test_resolveAlias(t *testing.T) {
	tests := []struct {
		name       string
		aliasDef   string
		wantExec   string
		wantParams []string
		wantErr    bool
	}{
		{
			name:    "empty definition",
			wantErr: true,
		},
		{
			name:     "bad escape",
			aliasDef: "echo \\",
			wantErr:  true,
		},
		{
			name:       "no args",
			aliasDef:   "echo",
			wantExec:   "echo",
			wantParams: []string{},
			wantErr:    false,
		},
		{
			name:       "bash arg",
			aliasDef:   "bash -c 'foo bar baz'",
			wantExec:   "bash",
			wantParams: []string{"-c", "foo bar baz"},
			wantErr:    false,
		},
		{
			name:       "several args",
			aliasDef:   "tsh login --foo --bar --baz $@",
			wantExec:   "tsh",
			wantParams: []string{"login", "--foo", "--bar", "--baz", "$@"},
			wantErr:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			executable, params, err := parseAliasDefinition(tt.aliasDef)

			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.wantExec, executable)
				require.Equal(t, tt.wantParams, params)
			}
		})
	}
}

func Test_runAliasCommand(t *testing.T) {
	type args struct {
		tshExecutable string
		aliasesSeen   []string
		executable    string
		arguments     []string
	}
	tests := []struct {
		name    string
		args    args
		wantErr bool
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := runAliasCommand(tt.args.tshExecutable, tt.args.aliasesSeen, tt.args.executable, tt.args.arguments); (err != nil) != tt.wantErr {
				t.Errorf("runAliasCommand() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func Test_tryRunAlias(t *testing.T) {
	type args struct {
		aliases       map[string]string
		tshExecutable string
		args          []string
		getEnv        func(key string) string
	}
	tests := []struct {
		name    string
		args    args
		want    bool
		wantErr bool
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tryRunAlias(tt.args.aliases, tt.args.tshExecutable, tt.args.args, tt.args.getEnv)
			if (err != nil) != tt.wantErr {
				t.Errorf("tryRunAlias() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("tryRunAlias() got = %v, want %v", got, tt.want)
			}
		})
	}
}
