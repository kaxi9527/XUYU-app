//go:build (darwin || linux) && cgo

package projectplacement

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/kaxi9527/WorkShelf/internal/projectmemory"
)

func TestJournalProcessClaimHelper(t *testing.T) {
	if os.Getenv("WORKSHELF_JOURNAL_TEST_CHILD") != "1" {
		return
	}
	var project projectmemory.Project
	if json.Unmarshal([]byte(os.Getenv("WORKSHELF_JOURNAL_TEST_PROJECT")), &project) != nil {
		t.Fatal("invalid fixture")
	}
	j, err := NewJournal(os.Getenv("WORKSHELF_JOURNAL_TEST_CONTROL"), project)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err = j.change(ctx, "operation-one", func(s *journalSession) error {
		_, err := s.claim(ctx, os.Getenv("WORKSHELF_JOURNAL_TEST_FILE"), time.Now())
		return err
	})
	if errors.Is(err, ErrReconcile) {
		fmt.Println("RESULT_RECONCILE")
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println("RESULT_CLAIM")
}

func TestJournalSeparateProcessesClaimAtMostOnce(t *testing.T) {
	j, p, ids := journalFixture(t)
	v := confirmed(t, j, p, ids)
	project, _ := json.Marshal(j.project)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	commands := []*exec.Cmd{}
	outputs := []*bytes.Buffer{}
	for i := 0; i < 2; i++ {
		command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestJournalProcessClaimHelper$")
		command.Env = append(os.Environ(), "WORKSHELF_JOURNAL_TEST_CHILD=1", "WORKSHELF_JOURNAL_TEST_PROJECT="+string(project), "WORKSHELF_JOURNAL_TEST_CONTROL="+j.control, "WORKSHELF_JOURNAL_TEST_FILE="+ids[0])
		output := &bytes.Buffer{}
		command.Stdout = output
		command.Stderr = output
		if err := command.Start(); err != nil {
			cancel()
			for _, previous := range commands {
				_ = previous.Wait()
			}
			t.Fatal(err)
		}
		commands = append(commands, command)
		outputs = append(outputs, output)
	}
	claimed, reconciled := 0, 0
	for i, command := range commands {
		if err := command.Wait(); err != nil {
			t.Errorf("owned subprocess failed: %v %s", err, outputs[i].String())
		}
		if strings.Contains(outputs[i].String(), "RESULT_CLAIM") {
			claimed++
		}
		if strings.Contains(outputs[i].String(), "RESULT_RECONCILE") {
			reconciled++
		}
	}
	if claimed != 1 || reconciled != 1 {
		t.Fatalf("claim=%d reconciliation=%d", claimed, reconciled)
	}
	view, err := j.Read(context.Background(), v.Intent.ID)
	if err != nil || view.Revision != 2 || view.Items[0].Stage != "claimed" || !view.NeedsReconciliation {
		t.Fatal("cross-process receipt lost", err)
	}
}
