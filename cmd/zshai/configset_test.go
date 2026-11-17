package main

import (
	"reflect"
	"testing"
)

func TestSplitPairs(t *testing.T) {
	for in, want := range map[string][][2]string{
		"fixAttempts=2":   {{"fixAttempts", "2"}},
		"fixAttempts = 2": {{"fixAttempts", "2"}},
		"typos av":        {{"typos", "av"}},
		"typos":           {{"typos", "\x00"}},
		"escalate.model=a/b escalate.server=http://x:1/v1": {{"escalate.model", "a/b"}, {"escalate.server", "http://x:1/v1"}},
		`notes=["a b", "c"] log=false`:                     {{"notes", `["a b", "c"]`}, {"log", "false"}},
		`model='qwen coder' theme=auto`:                    {{"model", "'qwen coder'"}, {"theme", "auto"}},
	} {
		got, err := splitPairs(in)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("splitPairs(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
}

func TestConfigArg(t *testing.T) {
	for in, want := range map[string]bool{
		"fixAttempts=2": true, "typos av": true, "typos": true, "escalate.model=x": true,
		"hvordan endrer jeg språket": false, "how do I change the model": false,
	} {
		if configArg.MatchString(in) != want {
			t.Errorf("configArg(%q) != %v", in, want)
		}
	}
}
