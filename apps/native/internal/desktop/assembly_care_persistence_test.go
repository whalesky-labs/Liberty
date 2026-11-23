package desktop

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/application"
	"liberty.local/liberty/internal/client"
	"liberty.local/liberty/internal/domain"
	"liberty.local/liberty/internal/infrastructure/repositories/jobdb"
)

func TestAssemblyCarePersistsTheSameCycleAsDirectCare(t *testing.T) {
	type outcome struct {
		stages   []domain.WorkGameTask
		finished domain.WorkGameState
		store    domain.PetStoreState
	}
	var direct outcome
	for _, mode := range []string{"direct care", "Steady Order", "Rush Order"} {
		t.Run(mode, func(t *testing.T) {
			const gameKey, taskID, jobKey = "factory", "factory-slot-1", "basic-assembly"
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), "assembly-care.sqlite3")
			store, _, err := jobdb.OpenPrepared(ctx, path, nil, true)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			started := time.Date(2026, 11, 20, 23, 59, 59, 0, time.UTC)
			now := started
			service := application.NewWork(store, func() time.Time { return now })
			initial, err := service.Start(ctx, gameKey, taskID, jobKey)
			if err != nil {
				t.Fatal(err)
			}
			job := initial.Tasks[0].Job
			if job == nil || job.JobKey != jobKey || job.CareRequired < 1 {
				t.Fatalf("fixture did not start the basic assembly task: %+v", initial.Tasks[0])
			}
			interval := time.Duration(job.DurationSeconds/(job.CareRequired+1)) * time.Second
			petState := func(database *jobdb.Store) domain.PetStoreState {
				t.Helper()
				state, err := database.PetStore(ctx, domain.PersistedUTC(started))
				if err != nil {
					t.Fatal(err)
				}
				return state
			}
			before := petState(store)
			dispatches := make(chan func(), 4)
			controller := client.NewWorkController(service, func(run func()) { dispatches <- run }, func() {})
			defer controller.Close()
			settle := func() {
				t.Helper()
				for controller.BusyKey != "" {
					receiveAssemblyDispatch(t, dispatches)()
				}
				if controller.Error != nil {
					t.Fatal(controller.Error)
				}
			}
			workspace := Workspace{view: &Meetings{Locale: "en-US"}, workClient: controller}
			workspace.workGame = &WorkGameView{Locale: "en-US", Client: controller, OpenAssemblyCare: workspace.openAssemblyCare}
			workspace.navigate(factoryRoute)
			settle()
			factory := ui.NewTester(workspace.workGame.View, 960, 700)
			openPractice := func() (*assemblyGameView, *ui.Tester) {
				t.Helper()
				factory.Frame()
				assemblyTestClick(t, factory, "Care with Assembly Practice")
				view := workspace.assemblyGame
				if workspace.tab != assemblyRoute || view == nil || view.CareTaskID != taskID {
					t.Fatal("optional care did not open for the selected task")
				}
				view.Locale = workspace.view.Locale
				return view, ui.NewTester(view.View, 960, 560)
			}
			result := outcome{}
			for stage := int64(0); stage < job.CareRequired; stage++ {
				now = now.Add(interval)
				controller.RefreshRoute()
				settle()
				awaitingCare := controller.GameStates[gameKey]
				if awaitingCare.Tasks[0].Status != workNeedsCare || awaitingCare.Tasks[0].StageIndex != stage {
					t.Fatalf("stage %d did not reach needs care: %+v", stage, awaitingCare.Tasks[0])
				}
				if mode != "direct care" {
					if stage == 0 {
						view, tester := openPractice()
						assemblyTestClick(t, tester, "Rush Order")
						assemblyTestClick(t, tester, "Start Assembly")
						wrong := domain.AssemblyPart(int(view.round.Snapshot().Order[0])%3 + 1)
						for range 9 {
							tester.Key(0, ui.Key1+ui.Key(wrong-1))
							tester.Frame()
						}
						if view.round.Snapshot().Phase != domain.AssemblyTimedOut {
							t.Fatal("fixture did not reach timeout")
						}
						assemblyTestClick(t, tester, "Play Again")
						assemblyTestClick(t, tester, "Pause")
						assemblyTestClick(t, tester, "Back to Factory")
						settle()
						if state := controller.GameStates[gameKey]; !reflect.DeepEqual(state, awaitingCare) || !reflect.DeepEqual(petState(store), before) {
							t.Fatal("timeout or abandoning a paused round changed the task or rewards")
						}
					}
					view, tester := openPractice()
					assemblyTestClick(t, tester, mode)
					assemblyTestClick(t, tester, "Start Assembly")
					order := view.round.Snapshot().Order
					if mode == "Rush Order" {
						// Complete with a different score, without changing the wall clock.
						wrong := domain.AssemblyPart(int(order[0])%3 + 1)
						assemblyTestClick(t, tester, fmt.Sprintf("%d · %s", wrong, view.partName(wrong)))
					}
					for _, part := range order {
						tester.Key(0, ui.Key1+ui.Key(part-1))
						tester.Frame()
					}
					if workspace.tab != factoryRoute || view.round.Snapshot().Phase != domain.AssemblyCompleted {
						t.Fatal("a completed practice did not return to the factory")
					}
				} else {
					factory.Frame()
					assemblyTestClick(t, factory, "Care Job")
				}
				settle()
				afterCare := controller.GameStates[gameKey]
				if afterCare.Tasks[0].Status != workRunning || afterCare.Tasks[0].StageIndex != stage+1 || len(afterCare.Rewards) != 0 || !reflect.DeepEqual(petState(store), before) {
					t.Fatal("care did not advance exactly once without creating rewards")
				}
				result.stages = append(result.stages, afterCare.Tasks[0])
			}

			now = now.Add(interval)
			controller.RefreshRoute()
			settle()
			factory.Frame()
			assemblyTestClick(t, factory, "Claim Reward")
			settle()
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := jobdb.Open(ctx, path)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			if _, err := reopened.ClaimWorkTask(ctx, gameKey, taskID, now); err == nil {
				t.Fatal("the same task could be claimed again after reopening")
			}
			result.finished, err = reopened.WorkGameState(ctx, gameKey, now)
			if err != nil {
				t.Fatal(err)
			}
			result.store = petState(reopened)
			if result.finished.Tasks[0].Status != workIdle || len(result.finished.Rewards) != 1 || len(result.store.Economy) != 1 {
				t.Fatal("claim did not persist exactly one reward with an idle slot and one LP entry")
			}
			reward := result.finished.Rewards[0]
			if reward.TaskID != taskID || reward.JobKey != jobKey || reward.LPReward < job.LPMin || reward.LPReward > job.LPMax || result.store.Wallet.Balance != before.Wallet.Balance+reward.LPReward {
				t.Fatalf("unexpected task or wallet after claiming: %+v", reward)
			}
			entry := &result.store.Economy[0]
			if entry.ID == "" || entry.SourceType != "work_game_reward" || entry.Amount != reward.LPReward || entry.BalanceAfter != result.store.Wallet.Balance {
				t.Fatal("claim did not persist the matching LP entry")
			}
			// Independent profiles allocate different LP entry IDs. The work
			// cycle key, reward ID, items, and all other fields must still match.
			entry.ID = ""
			if mode == "direct care" {
				direct = result
			} else if !reflect.DeepEqual(result, direct) {
				t.Fatal("the assembly mode or score changed the care stages, items, or LP compared with direct care")
			}
		})
	}
}
