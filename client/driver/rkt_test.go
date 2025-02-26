package driver

import (
	"fmt"
	"io/ioutil"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/hashicorp/nomad/client/config"
	"github.com/hashicorp/nomad/client/driver/env"
	"github.com/hashicorp/nomad/nomad/structs"
	"github.com/hashicorp/nomad/testutil"

	ctestutils "github.com/hashicorp/nomad/client/testutil"
)

func TestRktVersionRegex(t *testing.T) {
	input_rkt := "rkt version 0.8.1"
	input_appc := "appc version 1.2.0"
	expected_rkt := "0.8.1"
	expected_appc := "1.2.0"
	rktMatches := reRktVersion.FindStringSubmatch(input_rkt)
	appcMatches := reAppcVersion.FindStringSubmatch(input_appc)
	if rktMatches[1] != expected_rkt {
		fmt.Printf("Test failed; got %q; want %q\n", rktMatches[1], expected_rkt)
	}
	if appcMatches[1] != expected_appc {
		fmt.Printf("Test failed; got %q; want %q\n", appcMatches[1], expected_appc)
	}
}

// The fingerprinter test should always pass, even if rkt is not installed.
func TestRktDriver_Fingerprint(t *testing.T) {
	ctestutils.RktCompatible(t)
	driverCtx, _ := testDriverContexts(&structs.Task{Name: "foo"})
	d := NewRktDriver(driverCtx)
	node := &structs.Node{
		Attributes: make(map[string]string),
	}
	apply, err := d.Fingerprint(&config.Config{}, node)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !apply {
		t.Fatalf("should apply")
	}
	if node.Attributes["driver.rkt"] != "1" {
		t.Fatalf("Missing Rkt driver")
	}
	if node.Attributes["driver.rkt.version"] == "" {
		t.Fatalf("Missing Rkt driver version")
	}
	if node.Attributes["driver.rkt.appc.version"] == "" {
		t.Fatalf("Missing appc version for the Rkt driver")
	}
}

func TestRktDriver_Start(t *testing.T) {
	ctestutils.RktCompatible(t)
	// TODO: use test server to load from a fixture
	task := &structs.Task{
		Name: "etcd",
		Config: map[string]interface{}{
			"trust_prefix": "coreos.com/etcd",
			"image":        "coreos.com/etcd:v2.0.4",
			"command":      "/etcd",
		},
		LogConfig: &structs.LogConfig{
			MaxFiles:      10,
			MaxFileSizeMB: 10,
		},
		Resources: &structs.Resources{
			MemoryMB: 128,
			CPU:      100,
		},
	}

	driverCtx, execCtx := testDriverContexts(task)
	defer execCtx.AllocDir.Destroy()

	d := NewRktDriver(driverCtx)

	handle, err := d.Start(execCtx, task)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if handle == nil {
		t.Fatalf("missing handle")
	}
	defer handle.Kill()

	// Attempt to open
	handle2, err := d.Open(execCtx, handle.ID())
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if handle2 == nil {
		t.Fatalf("missing handle")
	}
}

func TestRktDriver_Start_Wait(t *testing.T) {
	ctestutils.RktCompatible(t)
	task := &structs.Task{
		Name: "etcd",
		Config: map[string]interface{}{
			"trust_prefix": "coreos.com/etcd",
			"image":        "coreos.com/etcd:v2.0.4",
			"command":      "/etcd",
			"args":         []string{"--version"},
		},
		LogConfig: &structs.LogConfig{
			MaxFiles:      10,
			MaxFileSizeMB: 10,
		},
		Resources: &structs.Resources{
			MemoryMB: 128,
			CPU:      100,
		},
	}

	driverCtx, execCtx := testDriverContexts(task)
	defer execCtx.AllocDir.Destroy()
	d := NewRktDriver(driverCtx)

	handle, err := d.Start(execCtx, task)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if handle == nil {
		t.Fatalf("missing handle")
	}
	defer handle.Kill()

	// Update should be a no-op
	err = handle.Update(task)
	if err != nil {
		t.Fatalf("err: %v", err)
	}

	select {
	case res := <-handle.WaitCh():
		if !res.Successful() {
			t.Fatalf("err: %v", res)
		}
	case <-time.After(time.Duration(testutil.TestMultiplier()*5) * time.Second):
		t.Fatalf("timeout")
	}
}

func TestRktDriver_Start_Wait_Skip_Trust(t *testing.T) {
	ctestutils.RktCompatible(t)
	task := &structs.Task{
		Name: "etcd",
		Config: map[string]interface{}{
			"image":   "coreos.com/etcd:v2.0.4",
			"command": "/etcd",
			"args":    []string{"--version"},
		},
		LogConfig: &structs.LogConfig{
			MaxFiles:      10,
			MaxFileSizeMB: 10,
		},
		Resources: &structs.Resources{
			MemoryMB: 128,
			CPU:      100,
		},
	}

	driverCtx, execCtx := testDriverContexts(task)
	defer execCtx.AllocDir.Destroy()
	d := NewRktDriver(driverCtx)

	handle, err := d.Start(execCtx, task)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if handle == nil {
		t.Fatalf("missing handle")
	}
	defer handle.Kill()

	// Update should be a no-op
	err = handle.Update(task)
	if err != nil {
		t.Fatalf("err: %v", err)
	}

	select {
	case res := <-handle.WaitCh():
		if !res.Successful() {
			t.Fatalf("err: %v", res)
		}
	case <-time.After(time.Duration(testutil.TestMultiplier()*5) * time.Second):
		t.Fatalf("timeout")
	}
}

func TestRktDriver_Start_Wait_AllocDir(t *testing.T) {
	ctestutils.RktCompatible(t)

	exp := []byte{'w', 'i', 'n'}
	file := "output.txt"

	task := &structs.Task{
		Name: "alpine",
		Config: map[string]interface{}{
			"image":        "docker://alpine",
			"command":      "/bin/sh",
			"args":         []string{
					"-c",
					fmt.Sprintf(`echo -n %s > ${%s}/%s`, string(exp), env.AllocDir, file),
			},
		},
		LogConfig: &structs.LogConfig{
			MaxFiles:      10,
			MaxFileSizeMB: 10,
		},
		Resources: &structs.Resources{
			MemoryMB: 128,
			CPU:      100,
		},
	}

	driverCtx, execCtx := testDriverContexts(task)
	defer execCtx.AllocDir.Destroy()
	d := NewRktDriver(driverCtx)

	handle, err := d.Start(execCtx, task)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if handle == nil {
		t.Fatalf("missing handle")
	}
	defer handle.Kill()

	select {
	case res := <-handle.WaitCh():
		if !res.Successful() {
			t.Fatalf("err: %v", res)
		}
	case <-time.After(time.Duration(testutil.TestMultiplier()*5) * time.Second):
		t.Fatalf("timeout")
	}

	// Check that data was written to the shared alloc directory.
        outputFile := filepath.Join(execCtx.AllocDir.SharedDir, file)
        act, err := ioutil.ReadFile(outputFile)
        if err != nil {
                t.Fatalf("Couldn't read expected output: %v", err)
        }

        if !reflect.DeepEqual(act, exp) {
                t.Fatalf("Command output is %v; expected %v", act, exp)
        }
}
