package main

import (
	"os"
	"testing"
)

func TestSproutFlagAndEnvironment(t *testing.T) {
	old, hadOld := os.LookupEnv("TUIOS_SPROUT")
	t.Cleanup(func() {
		if hadOld {
			_ = os.Setenv("TUIOS_SPROUT", old)
			return
		}
		_ = os.Unsetenv("TUIOS_SPROUT")
	})

	for _, test := range []struct {
		name string
		env  *string
		args []string
		want bool
	}{
		{name: "flag", args: []string{"--sprout"}, want: true},
		{name: "environment", env: stringPtr("1"), want: true},
		{name: "zero", env: stringPtr("0")},
		{name: "other", env: stringPtr("true")},
		{name: "unset"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if test.env == nil {
				_ = os.Unsetenv("TUIOS_SPROUT")
			} else if err := os.Setenv("TUIOS_SPROUT", *test.env); err != nil {
				t.Fatal(err)
			}
			root := newRootCommand()
			if err := root.ParseFlags(test.args); err != nil {
				t.Fatal(err)
			}
			if sproutMode != test.want {
				t.Errorf("sproutMode = %t, want %t", sproutMode, test.want)
			}
		})
	}
}

func stringPtr(s string) *string { return &s }
