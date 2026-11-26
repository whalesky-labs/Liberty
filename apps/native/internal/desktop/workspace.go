package desktop

import (
	"context"
	"runtime"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/application"
	"liberty.local/liberty/internal/client"
	"liberty.local/liberty/internal/desktop/presentation"
	"liberty.local/liberty/internal/domain"
	"liberty.local/liberty/internal/infrastructure/diagnosticlog"
)

const (
	factoryRoute  = "/work-game/factory"
	assemblyRoute = factoryRoute + "/assembly"
)

// Workspace owns native windows and their lifetimes, on MyGo's main thread.
// Database work and authorization checks run outside the render callback.
type Workspace struct {
	settings            *SettingsView
	editorViews         map[application.ResourceKind]*ResourceEditorView
	resultView          *ResultView
	summaryWindow       *mygo.Window
	summaryView         *SummaryView
	summaryTitle        string
	summaryService      *application.SummaryService
	notesView           *NotesView
	notesTitle          string
	notes               *mygo.Window
	resultEditing       bool
	wordExportEnabled   bool
	preferences         domain.UIPreferences
	main                *mygo.Window
	metrics             *client.MetricsController
	result              *mygo.Window
	controller          *client.Controller
	view                *Meetings
	detail              *Meetings
	meetings            client.Meetings
	jobDispatch         func(func())
	completionObserver  jobCompletionObserver
	completionMessage   string
	completionRevision  uint64
	completionTimer     *time.Timer
	processingModeError bool
	processingModeBusy  bool
	notificationCancel  context.CancelFunc
	showJobNotification func(string, string) context.CancelFunc
	dashboard           *DashboardView
	tab                 string
	lastAppPath         string
	lastDetailSource    string
	lastDetailID        string
	scopes              *application.WindowScopes
	openCancel          context.CancelFunc
	openRevision        uint64
	modeService         interface {
		ProcessingMode(context.Context) (string, error)
	}
	modeContext               context.Context
	modeCancel                context.CancelFunc
	modeDispatch              func(func())
	modeEpoch                 uint64
	modeRevision              uint64
	modeCapabilities          *client.RemoteController
	lastCapabilityCheck       time.Time
	newJobAction              func()
	closed                    bool
	closing                   closeCoordinator
	settingsClosing           settingsCloseGate
	runtime                   *RuntimeView
	live                      *LiveView
	newJob                    *NewJobView
	resources                 *ResourcesView
	resourceService           *application.Resources
	workClient                *client.WorkController
	workMarket                *WorkMarketView
	farm                      *FarmView
	workGame                  *WorkGameView
	assemblyGame              *assemblyGameView
	pendingAssemblyCareTaskID string
	pet                       *PetView
	itemWindow                *mygo.Window
	itemView                  *StoreItemView
	companions                *companionWindows
	stopThemeObserver         func()
	stopAppearanceObserver    func()
	editors                   map[application.ResourceKind]*mygo.Window
	openingEditor             map[application.ResourceKind]bool
	editorIDs                 map[application.ResourceKind]string
	pendingEditors            map[application.ResourceKind]string
}

func (w *Workspace) AttachRuntime(service client.Runtime) {
	controller := client.NewRuntime(service, mygo.RunOnMain, func() {
		if !w.closed {
			w.main.Invalidate()
		}
	})
	w.runtime = &RuntimeView{Client: controller, Locale: w.preferences.Locale, Back: w.returnToApp}
	controller.SetVisible(w.tab == "/settings" || w.tab == "/jobs/new")
	controller.Start()
}

func (w *Workspace) AttachPet(service *application.Pet, record ...func(diagnosticlog.Event, diagnosticlog.Instance)) {
	w.scopes.EnableExportRewards(service)
	controller := client.NewPet(service, mygo.RunOnMain, func() {
		if !w.closed {
			w.companions.sync()
			w.refreshStoreItem()
			w.main.Invalidate()
		}
	})
	w.pet = &PetView{Client: controller, Locale: w.preferences.Locale, Route: "/pet", Navigate: w.navigate, DesktopAvailable: true}
	controller.SetVisible(PetRouteAvailable(w.tab))
	w.companions = newCompanions(w.pet, service.SavePosition, record...)
	w.pet.OpenItem = w.openStoreItem
	w.pet.VisibleCompanions = w.companions.visibleCount
	w.pet.OpenExtra = w.companions.openExtra
	observe := func() { controller.ObserveAppearance(mygo.Theme.IsDark(), time.Now()) }
	w.stopThemeObserver = mygo.Theme.OnUpdated(observe)
	observe()
	controller.Start()
}

func (w *Workspace) AttachWork(service *application.Work) {
	changed := func() {
		if !w.closed {
			w.main.Invalidate()
		}
	}
	w.workClient = client.NewWorkController(service, mygo.RunOnMain, changed)
	w.workMarket = &WorkMarketView{Client: w.workClient, Locale: w.view.Locale, Open: w.navigate}
	w.farm = &FarmView{Client: w.workClient, Locale: w.view.Locale, Back: func() { w.navigate("/work-market") }, Choose: w.chooseCrop}
	w.workGame = &WorkGameView{Client: w.workClient, Locale: w.view.Locale, Back: func() { w.navigate("/work-market") }, OpenAssembly: func() { w.openAssemblyPractice() }, OpenAssemblyCare: w.openAssemblyCare}
	w.workClient.StartPolling()
}

func (w *Workspace) pauseAssemblyGame() {
	if w.assemblyGame != nil {
		w.assemblyGame.pause()
		if w.main != nil {
			w.main.Invalidate()
		}
	}
}

func (w *Workspace) openAssemblyPractice() {
	w.pendingAssemblyCareTaskID = ""
	w.assemblyGame = nil
	w.navigate(assemblyRoute)
}

func (w *Workspace) openAssemblyCare(taskID string) {
	if taskID == "" || w.closed || w.workClient == nil || w.workClient.BusyKey != "" {
		return
	}
	w.pendingAssemblyCareTaskID = taskID
	w.assemblyGame = nil
	w.navigate(assemblyRoute)
}

func (w *Workspace) completeAssemblyCare(taskID string) {
	if taskID == "" || w.closed || w.workClient == nil {
		return
	}
	w.workClient.CareForRoute("factory", taskID, factoryRoute)
	w.navigate(factoryRoute)
}

func (w *Workspace) chooseCrop(plot domain.FarmPlot, crops []domain.FarmCropConfig) {
	if len(crops) == 0 || w.closed {
		return
	}
	locale, parent := w.preferences.Locale, w.main
	buttons := make([]string, len(crops)+1)
	buttons[0] = presentation.Message(locale, "common.cancel")
	for i, crop := range crops {
		buttons[i+1] = workText(locale, crop.NameEn, crop.NameZh)
	}
	go func() {
		result, err := mygo.Dialog.Message(mygo.MessageOptions{Parent: parent, Type: mygo.MessageInfo, Title: workText(locale, "Choose Crop", "选择作物"), Message: workText(locale, "Select seeds for this plot.", "选择要种在这块地里的种子。"), Buttons: buttons, CancelButton: 0, DefaultButton: 0})
		if err != nil || result.Button <= 0 || result.Button > len(crops) {
			return
		}
		crop := crops[result.Button-1]
		mygo.RunOnMain(func() {
			if !w.closed && w.workClient != nil {
				w.workClient.Plant(plot.ID, crop.CropKey)
			}
		})
	}()
}

func (w *Workspace) AttachJobs(submission *application.Submission, deleter *application.Deleter, grants MediaGrants, actions ...client.JobRetry) {
	w.attachJobs(submission, deleter, grants, mygo.RunOnMain, actions...)
}

func (w *Workspace) attachJobs(submission *application.Submission, deleter *application.Deleter, grants MediaGrants, dispatch func(func()), actions ...client.JobRetry) {
	w.jobDispatch = dispatch
	w.view.confirmLocalRuntime = w.confirmJobRuntime
	changed := func() {
		if !w.closed && w.main != nil {
			w.main.Invalidate()
		}
	}
	c := client.NewSubmission(submission, &NativeMediaPicker{Parent: w.main, Grants: grants}, dispatch, changed, func(id string) { w.openDetailFromSource(id, "local") })
	w.newJob = &NewJobView{Client: c, Locale: w.preferences.Locale, WarnRuntime: w.warnJobRuntime}
	if w.runtime != nil {
		w.newJob.Runtime = w.runtime.Client
	}
	w.newJobAction = func() { w.navigate("/jobs/new") }
	w.applyJobMode(w.view.Source)
	var retryPort client.JobRetry = submission
	var deletePort client.JobDelete = deleter
	if len(actions) > 0 {
		retryPort = actions[0]
		if sourceDelete, ok := actions[0].(client.JobDelete); ok {
			deletePort = sourceDelete
		}
	}
	w.view.Actions = client.NewJobActions(retryPort, deletePort, func(ctx context.Context, title string) (bool, error) {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		result, err := mygo.Dialog.Message(mygo.MessageOptions{Parent: w.main, Type: mygo.MessageWarning, Title: presentation.Message(w.preferences.Locale, "jobs.deleteTitle"), Message: presentation.Format(w.preferences.Locale, "jobs.deleteConfirm", map[string]any{"title": title}), Buttons: []string{presentation.Message(w.preferences.Locale, "common.cancel"), presentation.Message(w.preferences.Locale, "common.delete")}, CancelButton: 0, DefaultButton: 0, DestructiveButtons: []int{1}})
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		return result.Button == 1, err
	}, dispatch, changed, w.completeJobAction)
	w.view.Actions.Source = w.view.Source
	w.controller.StartPolling()
}

func (w *Workspace) completeJobAction(source, id string, deleted bool) {
	if deleted && source == w.view.Source && w.controller.SelectedID == id {
		w.controller.Select("")
		if w.detail == nil && w.tab == "/jobs/"+id {
			w.navigate("/jobs")
		}
	}
	if w.detail != nil && w.detail.Source == source && w.detail.Client.SelectedID == id {
		if deleted {
			w.navigate("/jobs")
		} else {
			w.detail.Client.ResultController.Refresh()
		}
	}
	w.controller.ResultController.Refresh()
	w.controller.Poll()
}

func (w *Workspace) AttachMode(service interface {
	ProcessingMode(context.Context) (string, error)
}) {
	w.modeService = service
	if w.modeCapabilities != nil {
		w.modeCapabilities.Close()
		w.modeCapabilities = nil
	}
	if capabilities, ok := service.(client.RemoteCapabilitiesService); ok {
		w.modeCapabilities = client.NewRemote(capabilities, mygo.RunOnMain, func() {
			if w.closed {
				return
			}
			w.view.RemoteCapabilities = w.modeCapabilities.Capabilities
			w.dashboard.RemoteCapabilities = w.modeCapabilities.Capabilities
			w.main.Invalidate()
		})
	}
	if w.modeCancel != nil {
		w.modeCancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	w.modeCancel, w.modeContext = cancel, ctx
	w.modeEpoch++
	w.processingModeBusy = false
	epoch := w.modeEpoch
	w.modeDispatch = mygo.RunOnMain
	w.refreshProcessingMode()
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				mygo.RunOnMain(func() {
					if !w.closed && w.modeEpoch == epoch && ctx.Err() == nil {
						w.refreshProcessingMode()
					}
				})
			}
		}
	}()
}

func (w *Workspace) refreshProcessingMode() {
	if w.closed || w.modeService == nil || w.modeContext == nil || w.modeContext.Err() != nil || w.processingModeBusy {
		return
	}
	ctx, service := w.modeContext, w.modeService
	epoch, revision := w.modeEpoch, w.modeRevision
	w.processingModeBusy = true
	if w.main != nil && w.processingModeError {
		w.main.Invalidate()
	}
	go func() {
		mode, err := service.ProcessingMode(ctx)
		if ctx.Err() != nil {
			return
		}
		dispatch := w.modeDispatch
		if dispatch == nil {
			dispatch = mygo.RunOnMain
		}
		dispatch(func() {
			w.completeProcessingModeRefresh(ctx, epoch, revision, mode, err)
		})
	}()
}

func (w *Workspace) completeProcessingModeRefresh(ctx context.Context, epoch, revision uint64, mode string, err error) {
	if w.closed || ctx.Err() != nil || w.modeEpoch != epoch {
		return
	}
	w.processingModeBusy = false
	previousError, previousMode := w.processingModeError, w.view.Source
	if w.modeRevision == revision {
		w.applyProcessingModeResult(mode, err)
	}
	if w.main != nil && (previousError || w.processingModeError || previousMode != w.view.Source) {
		w.main.Invalidate()
	}
}

func (w *Workspace) retryProcessingMode() {
	if w.processingModeError {
		w.refreshProcessingMode()
	}
}

func (w *Workspace) applySavedSettings(settings domain.Settings) {
	if w.closed || settings.ProcessingMode != "local" && settings.ProcessingMode != "remote" {
		return
	}
	w.modeRevision++
	w.applyProcessingMode(settings.ProcessingMode)
}

func (w *Workspace) applyProcessingModeResult(mode string, err error) {
	if err != nil || mode != "local" && mode != "remote" {
		w.processingModeError = true
		if w.main != nil {
			w.main.Invalidate()
		}
		return
	}
	w.applyProcessingMode(mode)
}

func (w *Workspace) applyProcessingMode(mode string) {
	if w.view == nil || mode != "local" && mode != "remote" {
		return
	}
	w.processingModeError = false
	w.refreshModeCapabilities(mode)
	w.applyJobMode(mode)
	if w.view.Source == mode {
		if mode == "remote" && w.dashboard != nil && w.dashboard.Client != nil && (w.dashboard.Client.Overview == nil || w.dashboard.Client.Overview.Source != mode) {
			w.dashboard.Client.Overview = nil
			w.dashboard.Client.Refresh(w.dashboard.Period())
		}
		return
	}
	w.view.Source = mode
	if w.view.Actions != nil {
		w.view.Actions.Source = mode
	}
	w.view.page, w.view.selected = 1, -1
	if w.controller != nil {
		w.controller.Select("")
		w.controller.Refresh(domain.QueueQuery{Source: mode, Filter: w.view.status, Search: w.view.query, Page: w.view.page})
	}
	if w.dashboard != nil && w.dashboard.Client != nil {
		w.dashboard.Client.Overview = nil
		w.dashboard.Client.Error = nil
		w.dashboard.Client.Refresh(w.dashboard.Period())
	}
	if w.main != nil {
		w.main.Invalidate()
	}
}

func (w *Workspace) applyJobMode(mode string) {
	if w.newJob == nil {
		return
	}
	w.newJob.Remote = mode == "remote"
	w.newJob.Client.Unavailable = w.newJob.Remote
	w.view.NewJob = w.newJobAction
	if w.newJob.Remote {
		w.view.NewJob = nil
	}
}
func (w *Workspace) refreshModeCapabilities(mode string) {
	if w.modeCapabilities == nil {
		return
	}
	if w.view.Source != mode {
		w.modeCapabilities.Reset()
		w.lastCapabilityCheck = time.Time{}
	}
	if mode == "remote" && !w.modeCapabilities.Checking && time.Since(w.lastCapabilityCheck) >= 10*time.Second {
		w.lastCapabilityCheck = time.Now()
		w.modeCapabilities.Refresh()
	}
}

func (w *Workspace) onJobsChanged() {
	if w.closed {
		return
	}
	if w.pet != nil && w.controller != nil {
		environment := petEnvironment(w.controller.Jobs)
		if w.pet.Environment != environment {
			w.pet.Environment = environment
			if w.companions != nil {
				w.companions.invalidate()
			}
		}
	}
	if w.controller != nil && !w.controller.LoadingJobs && w.controller.ListError == nil {
		for _, job := range w.completionObserver.Observe(w.controller.Source, w.controller.Jobs, true) {
			w.presentJobCompletion(job)
		}
	}
	if w.main != nil {
		w.main.Invalidate()
	}
}

func workspaceWindowOptionsForPlatform(platform string, preferences domain.UIPreferences, content func(*ui.Context), systemDark ...bool) mygo.WindowOptions {
	options := mainWindowOptionsForPlatform(platform, content)
	if options.Vibrancy != mygo.VibrancyNone {
		options.Vibrancy, options.VibrancyTint = windowGlass(preferences, len(systemDark) > 0 && systemDark[0])
	}
	if platform == "windows" {
		options.TitleBarStyle = mygo.TitleBarDefault
		options.TitleBarHeight = 0
	}
	return options
}

func OpenWorkspace(service client.Meetings, dashboard *application.Dashboard, preferences domain.UIPreferences, initialSource ...string) *Workspace {
	locale := preferences.Locale
	w := &Workspace{scopes: application.NewWindowScopes(service), meetings: service, tab: "/", preferences: preferences}
	w.showJobNotification = nativeJobNotification
	w.controller = client.New(service, mygo.RunOnMain, w.onJobsChanged)
	w.view = NewMeetings(w.controller, locale)
	if len(initialSource) > 0 && (initialSource[0] == "local" || initialSource[0] == "remote") {
		w.view.Source = initialSource[0]
	}
	w.view.OpenResult = func(id, locale string) { w.openResultFromSource(id, locale, w.view.Source) }
	w.view.OpenDetail = w.openDetail
	dashboardClient := client.NewDashboard(dashboard, mygo.RunOnMain, func() {
		if !w.closed && w.main != nil {
			w.main.Invalidate()
		}
	})
	w.dashboard = NewDashboardView(dashboardClient, locale)
	w.dashboard.OpenResultFromSource = func(source, id, locale string) {
		w.openResultFromSource(id, locale, source)
	}
	w.dashboard.OpenDetailFromSource = func(source, id string) {
		w.openDetailFromSource(id, source)
	}
	w.dashboard.Navigate = w.navigate
	w.dashboard.CanNavigate = w.canNavigate
	w.main = mygo.NewWindow(workspaceWindowOptionsForPlatform(runtime.GOOS, preferences, w.View, mygo.Theme.IsDark()))
	w.stopAppearanceObserver = mygo.Theme.OnUpdated(func() {
		if !w.closed {
			applyWindowGlass(w.main, w.preferences)
		}
	})
	w.installCloseGuards()
	w.main.OnBlur(w.pauseAssemblyGame)
	w.main.OnHide(w.pauseAssemblyGame)
	w.main.OnMinimize(w.pauseAssemblyGame)
	w.main.OnClosed(func() {
		w.closed = true
		w.stopAppearanceObserver()
		w.stopJobCompletionFeedback()
		w.assemblyGame = nil
		w.controller.Close()
		w.closeDetail()
		dashboardClient.Close()
		if w.openCancel != nil {
			w.openCancel()
		}
		if w.modeCancel != nil {
			w.modeCancel()
		}
		w.processingModeBusy = false
		if w.modeCapabilities != nil {
			w.modeCapabilities.Close()
		}
		if w.runtime != nil {
			w.runtime.Client.Close()
		}
		if w.live != nil {
			w.live.Client.Close()
		}
		if w.workClient != nil {
			w.workClient.Close()
		}
		if w.pet != nil {
			w.stopThemeObserver()
			w.pet.Client.Close()
			w.companions.close()
		}
		if w.newJob != nil {
			w.newJob.Client.Close()
		}
		if w.view.Actions != nil {
			w.view.Actions.Close()
		}
		if w.settings != nil {
			w.settings.Client.Close()
			w.settings.CloseAuxiliary()
		}
		if w.resources != nil {
			w.resources.Client.Close()
		}
		for _, window := range w.editors {
			window.Close()
		}
		if w.itemWindow != nil {
			w.itemWindow.Close()
		}
		if w.summaryWindow != nil {
			w.summaryWindow.Close()
		}
		if w.result != nil {
			w.result.Close()
		}
		if w.notes != nil {
			w.notes.Close()
		}
	})
	w.controller.Refresh(domain.QueueQuery{Source: w.view.Source, Filter: "all", Page: 1})
	dashboardClient.Refresh("7d")
	return w
}

func (w *Workspace) canNavigate(path string) bool {
	path = queueRedirect(path)
	if path == "/live" {
		return w.live != nil
	}
	if w.pet != nil && PetRouteAvailable(path) {
		return true
	}
	if path == "/jobs/new" && w.view.Source == "remote" {
		return false
	}
	if w.workClient != nil && (path == "/work-market" || path == "/farm" || strings.HasPrefix(path, "/work-game/")) {
		return true
	}
	if w.resources != nil && (path == "/models" || path == "/templates" || path == "/members") {
		return true
	}
	_, queue := queueFilter(path)
	return path == "/" || queue || (path == "/settings" && w.runtime != nil) || (path == "/jobs/new" && w.newJob != nil)
}

func queueRedirect(path string) string {
	pathname, _, _ := strings.Cut(path, "?")
	if pathname == "/results" {
		return "/jobs?status=completed"
	}
	if job, ok := strings.CutPrefix(pathname, "/jobs/"); ok {
		if id, ok := strings.CutSuffix(job, "/workbench"); ok && id != "" && !strings.Contains(id, "/") {
			return "/jobs?status=completed"
		}
	}
	return path
}

func queueFilter(path string) (string, bool) {
	if path == "/jobs" {
		return "all", true
	}
	filter, ok := strings.CutPrefix(path, "/jobs?status=")
	if ok && (filter == "processing" || filter == "completed" || filter == "failed") {
		return filter, true
	}
	return "all", false
}

func (w *Workspace) navigate(path string) {
	path = queueRedirect(path)
	pathname := strings.Split(path, "?")[0]
	if pathname == "/settings" && w.tab != "/settings" {
		w.lastAppPath = w.tab
		if w.tab == "/jobs" && w.view != nil && w.view.status != "all" && w.view.status != "" {
			w.lastAppPath += "?status=" + w.view.status
		}
		w.lastDetailSource, w.lastDetailID = "", ""
		if w.detail != nil && w.detail.Client != nil && w.detail.Client.SelectedID != "" {
			w.lastDetailSource = w.detail.Source
			w.lastDetailID = w.detail.Client.SelectedID
		}
	}
	if w.detail != nil && pathname != "/jobs/"+w.detail.Client.SelectedID {
		w.closeDetail()
	}
	if w.settings != nil && w.tab == "/settings" && w.settings.Client.Dirty() {
		w.settings.Client.Save()
	}
	if w.tab == "/settings" && strings.Split(path, "?")[0] != "/settings" {
		if w.settings != nil {
			w.settings.search = ""
		}
		if w.runtime != nil {
			w.runtime.search = ""
		}
	}
	w.tab = strings.Split(path, "?")[0]
	if w.runtime != nil {
		w.runtime.Client.SetVisible(w.tab == "/settings" || w.tab == "/jobs/new")
	}
	if w.live != nil {
		w.live.Client.SetVisible(w.tab == "/live")
	}
	if w.pet != nil {
		w.pet.Client.SetVisible(PetRouteAvailable(w.tab) || w.itemWindow != nil)
	}
	if w.tab == assemblyRoute && w.workGame != nil {
		if w.assemblyGame == nil {
			taskID := w.pendingAssemblyCareTaskID
			w.pendingAssemblyCareTaskID = ""
			w.assemblyGame = &assemblyGameView{
				Back:       func() { w.navigate(factoryRoute) },
				CareTaskID: taskID,
				OnCareComplete: func() {
					w.completeAssemblyCare(taskID)
				},
			}
		}
	} else {
		w.assemblyGame = nil
	}
	if w.pet != nil && PetRouteAvailable(w.tab) {
		w.pet.Route = w.tab
		w.pet.Client.Refresh()
	}
	if w.workClient != nil {
		if w.workMarket != nil {
			w.workMarket.Locale = w.view.Locale
		}
		if w.farm != nil {
			w.farm.Locale = w.view.Locale
		}
		if w.workGame != nil {
			w.workGame.Locale = w.view.Locale
		}
		if w.tab == assemblyRoute {
			w.workClient.SetRoute("")
		} else {
			w.workClient.SetRoute(w.tab)
		}
	}
	if w.workGame != nil && w.tab != assemblyRoute && strings.HasPrefix(w.tab, "/work-game/") {
		w.workGame.GameKey = strings.TrimPrefix(w.tab, "/work-game/")
	}
	if w.tab == "/jobs/new" && w.newJob != nil {
		w.newJob.Remote = w.view.Source == "remote"
		w.newJob.Client.Unavailable = w.newJob.Remote
		w.newJob.Client.RefreshDefaults()
	}
	if w.resources != nil {
		kind := application.ResourceKind(strings.TrimPrefix(w.tab, "/"))
		if kind == application.Models || kind == application.Templates || kind == application.Members {
			w.resources.Kind = kind
			w.resources.Client.Refresh(kind)
		}
	}
	if w.tab == "/jobs" {
		w.view.status, _ = queueFilter(path)
		w.view.page = 1
		w.view.refresh()
	}
}

func (w *Workspace) returnToApp() {
	path := w.lastAppPath
	if path == "" || path == "/settings" || path == "/jobs/new" && w.view.Source == "remote" {
		w.lastDetailSource, w.lastDetailID = "", ""
		path = "/"
	}
	if w.lastDetailID != "" && path == "/jobs/"+w.lastDetailID {
		source, id := w.lastDetailSource, w.lastDetailID
		w.lastDetailSource, w.lastDetailID = "", ""
		w.openDetailFromSource(id, source)
		return
	}
	w.lastDetailSource, w.lastDetailID = "", ""
	w.navigate(path)
}

func (w *Workspace) shellStatus() (string, string) {
	if w.live != nil && domain.LiveActive(w.live.Client.Snapshot.Session.State) {
		return liveStatus(w.view.Locale, w.live.Client.Snapshot.Session.State), "info"
	}
	statusMessage, statusTone := w.completionMessage, "success"
	if w.processingModeError {
		locale := w.preferences.Locale
		if w.view != nil && w.view.Locale != "" {
			locale = w.view.Locale
		}
		statusMessage, statusTone = presentation.Message(locale, "shell.modeReadFailed"), "error"
	}
	return statusMessage, statusTone
}

func (w *Workspace) openDetail(id string) {
	w.openDetailFromSource(id, w.view.Source)
}
func (w *Workspace) View(c *ui.Context) {
	statusMessage, statusTone := w.shellStatus()
	shell := Shell{Locale: w.view.Locale, Route: w.tab, Source: w.view.Source, Version: presentation.Version, Preferences: w.preferences, ActiveJobs: w.controller.Page.Counts.Processing, StatusMessage: statusMessage, StatusTone: statusTone, ModeReadFailed: w.processingModeError, ModeReadBusy: w.processingModeBusy, RetryMode: w.retryProcessingMode,
		Navigate: w.navigate, Available: w.canNavigate, Content: func(c *ui.Context) {
			switch {
			case w.tab == "/live" && w.live != nil:
				w.live.Locale = w.view.Locale
				w.live.View(c)
			case w.pet != nil && PetRouteAvailable(w.tab):
				w.pet.View(c)
			case w.tab == "/work-market" && w.workMarket != nil:
				w.workMarket.View(c)
			case w.tab == "/farm" && w.farm != nil:
				w.farm.View(c)
			case w.tab == assemblyRoute && w.assemblyGame != nil:
				w.assemblyGame.Locale = w.view.Locale
				w.assemblyGame.View(c.Key(assemblyRoute))
			case strings.HasPrefix(w.tab, "/work-game/") && w.workGame != nil:
				w.workGame.View(c)
			case (w.tab == "/models" || w.tab == "/templates" || w.tab == "/members") && w.resources != nil:
				w.resources.View(c)
			case w.tab == "/jobs/new" && w.newJob != nil:
				w.newJob.View(c)
			case w.tab == "/settings" && w.settings != nil:
				w.settings.View(c)
			case w.tab == "/settings" && w.runtime != nil:
				w.runtime.View(c)
			case w.tab == "/jobs":
				w.view.View(c)
			case strings.HasPrefix(w.tab, "/jobs/"):
				detail := w.detail
				if detail == nil {
					detail = w.view
				}
				detail.Locale, detail.RemoteCapabilities = w.view.Locale, w.view.RemoteCapabilities
				detail.Actions = w.view.Actions
				detail.OpeningResult, detail.OpenError = w.view.OpeningResult, w.view.OpenError
				detail.Detail(c, func() { w.navigate("/jobs") })
			default:
				w.dashboard.Locale = w.view.Locale
				w.dashboard.View(c)
			}
		}}
	if shell.Source == "remote" {
		shell.SourceReady = w.modeCapabilities != nil && w.modeCapabilities.Capabilities != nil
	} else if w.runtime != nil && w.runtime.Client.Panel != nil {
		shell.SourceReady = w.runtime.Client.Error == nil && w.runtime.Client.Panel.Overview.ShellReady
	}
	if w.dashboard != nil {
		w.dashboard.RuntimeReady = shell.Source == "local" && shell.SourceReady
	}
	if w.tab == "/settings" && w.runtime != nil {
		shell.Sidebar = w.runtime.Sidebar
		if w.settings != nil {
			shell.Sidebar = w.settings.Sidebar
		}
	}
	if w.pet != nil && w.pet.Client.Settings != nil {
		settings := *w.pet.Client.Settings
		shell.PetEnabled = settings.DesktopEnabled
		if !w.pet.Client.Busy {
			shell.TogglePet = func() {
				settings.DesktopEnabled = !settings.DesktopEnabled
				w.pet.Client.SaveSettings(settings)
			}
		}
	}
	if w.metrics != nil {
		shell.Metrics = w.metrics.Value
	}
	shell.View(c)
}

func (w *Workspace) openResult(id, locale string) {
	w.openResultFromSource(id, locale, w.view.Source)
}

func (w *Workspace) openResultFromSource(id, locale, source string) {
	if w.closed {
		return
	}
	boundSource := source
	if w.openCancel != nil {
		w.openCancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	w.openCancel = cancel
	w.openRevision++
	revision := w.openRevision
	w.view.OpeningResult, w.view.OpenError = true, false
	go func() {
		grant, err := w.scopes.OpenFromMain(ctx, application.Workbench, boundSource, id)
		mygo.RunOnMain(func() {
			if w.closed || revision != w.openRevision {
				if grant != nil {
					grant.Close()
				}
				return
			}
			w.view.OpeningResult, w.view.OpenError = false, err != nil
			w.main.Invalidate()
			if err != nil {
				return
			}
			if w.result != nil {
				w.result.Close()
			}
			var window *mygo.Window
			controller := client.NewResult(grant, mygo.RunOnMain, func() {
				if window != nil {
					window.Invalidate()
				}
			})
			// The lookup is asynchronous; preferences may have changed since it began.
			locale = w.preferences.Locale
			view := NewResultView(controller, locale)
			w.resultView = view
			view.Preferences = w.preferences
			window = mygo.NewWindow(mygo.WindowOptions{
				Title: resultWindowTitle(locale), Width: 1280, Height: 900, MinWidth: 960, MinHeight: 720,
				Content: ui.View(view.View),
			})
			view.Close = window.Close
			view.OpenNotes = func(title string) { w.openNotes(grant, id, title) }
			if w.summaryService != nil {
				view.OpenSummary = func(title string) { w.openSummary(grant, title) }
			}
			view.Actions = w.resultActions(window, view, grant)
			view.EditingEnabled = w.resultEditing
			view.WordExportEnabled = w.wordExportEnabled
			w.result = window
			closeCapabilities := func() {}
			if boundSource == "remote" {
				closeCapabilities = startResultCapabilities(grant, view, window)
			}
			window.OnClosed(func() {
				closeCapabilities()
				view.Actions.Close()
				controller.Close()
				grant.Close()
				if w.result == window {
					w.result = nil
					w.resultView = nil
				}
			})
			controller.Select(id)
			controller.StartPolling()
		})
	}()
}
