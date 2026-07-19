package web

import (
	"strings"
	"testing"
)

func TestStylesForceSquareUI(t *testing.T) {
	data, err := Assets.ReadFile("static/styles.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(data)
	if !strings.Contains(css, "border-radius: 0 !important") {
		t.Fatal("styles must force square UI with border-radius: 0 !important")
	}
	for _, line := range strings.Split(css, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "border-radius:") && !strings.HasPrefix(line, "border-radius: 0") {
			t.Fatalf("found non-zero border radius declaration: %s", line)
		}
	}
}

func TestIndexIsTheManagementApp(t *testing.T) {
	data, err := Assets.ReadFile("static/index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(data)
	for _, want := range []string{"Dockertree", "Admin token", "扫描", "projects", "projectPagination", "themeToggle"} {
		if !strings.Contains(html, want) {
			t.Fatalf("index.html missing %q", want)
		}
	}
}

func TestDashboardUsesClickSwitchedViews(t *testing.T) {
	indexData, err := Assets.ReadFile("static/index.html")
	if err != nil {
		t.Fatal(err)
	}
	appData, err := Assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	cssData, err := Assets.ReadFile("static/styles.css")
	if err != nil {
		t.Fatal(err)
	}
	combined := string(indexData) + string(appData) + string(cssData)
	for _, want := range []string{"viewTabs", `data-view="containers"`, `data-view="projects"`, `data-view="images"`, `data-view="deploy"`, "containersView", "projectsView", "imagesView", "deployView", "view-panel", "setActiveView"} {
		if !strings.Contains(combined, want) {
			t.Fatalf("click-switched dashboard missing %q", want)
		}
	}
	if strings.Contains(string(cssData), ".dashboard-grid") {
		t.Fatal("dashboard should use click-switched views, not simultaneous grid columns")
	}
}

func TestDeployUIIsOwnSwitchedView(t *testing.T) {
	indexData, err := Assets.ReadFile("static/index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(indexData)
	for _, want := range []string{`data-view="deploy"`, `id="deployView"`, `data-view-panel="deploy"`, "部署"} {
		if !strings.Contains(html, want) {
			t.Fatalf("deploy view missing %q", want)
		}
	}
	imagesStart := strings.Index(html, `id="imagesView"`)
	deployStart := strings.Index(html, `id="deployView"`)
	if imagesStart == -1 || deployStart == -1 || deployStart <= imagesStart {
		t.Fatal("deploy view should be a separate panel after images view")
	}
	imagesSection := html[imagesStart:deployStart]
	for _, forbidden := range []string{"deploy-body", "deployModeImage", "composeDeployForm"} {
		if strings.Contains(imagesSection, forbidden) {
			t.Fatalf("images view should not contain deploy UI %q", forbidden)
		}
	}
}

func TestAllListsArePaginatedAtTenRows(t *testing.T) {
	indexData, err := Assets.ReadFile("static/index.html")
	if err != nil {
		t.Fatal(err)
	}
	appData, err := Assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	combined := string(indexData) + string(appData)
	for _, want := range []string{"containerPagination", "projectPagination", "imagePagination", "imageSearchPagination", "servicePagination", "pageSize: 10", "renderPagination", "pageItems", "renderImageSearchResults"} {
		if !strings.Contains(combined, want) {
			t.Fatalf("pagination UI missing %q", want)
		}
	}
}

func TestProjectDetailExpandsInsideSelectedProjectRow(t *testing.T) {
	indexData, err := Assets.ReadFile("static/index.html")
	if err != nil {
		t.Fatal(err)
	}
	appData, err := Assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	html := string(indexData)
	js := string(appData)
	if strings.Contains(html, `id="detail"`) {
		t.Fatal("project detail should not be a separate fixed panel outside the project list")
	}
	for _, want := range []string{"project-entry", "project-detail", "renderProjectDetail(project)", "entry.appendChild(renderProjectDetail(project))"} {
		if !strings.Contains(js, want) {
			t.Fatalf("inline project detail missing %q", want)
		}
	}
}

func TestProjectListDoesNotAutoExpandFirstProjectOnLoad(t *testing.T) {
	appData, err := Assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(appData)
	start := strings.Index(js, "async function loadProjects()")
	end := strings.Index(js, "function reconcileSelectedProject()")
	if start == -1 || end == -1 || end <= start {
		t.Fatal("could not find loadProjects section")
	}
	loadProjects := js[start:end]
	if strings.Contains(loadProjects, "state.projects[0]") {
		t.Fatalf("loadProjects should not auto-expand the first project: %s", loadProjects)
	}
	for _, want := range []string{"if (state.selected)", "selectProject(state.selected)", "renderProjects()"} {
		if !strings.Contains(loadProjects, want) {
			t.Fatalf("loadProjects should preserve only explicit selection; missing %q in %s", want, loadProjects)
		}
	}
}

func TestProjectDetailCanCollapseOnSecondClick(t *testing.T) {
	appData, err := Assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(appData)
	for _, want := range []string{"toggleProject(project.id)", "function toggleProject(id)", "if (state.selected === id)", "state.selected = null", "state.pagination.services = 1", "renderProjects()"} {
		if !strings.Contains(js, want) {
			t.Fatalf("project detail collapse behavior missing %q", want)
		}
	}
}

func TestContainerRowsShowPointerCursor(t *testing.T) {
	cssData, err := Assets.ReadFile("static/styles.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(cssData)
	start := strings.Index(css, ".project-item,\n.container-item")
	end := strings.Index(css[start:], ".project-item.active")
	if start == -1 || end == -1 {
		t.Fatal("could not find shared project/container item rule")
	}
	itemRule := css[start : start+end]
	if !strings.Contains(itemRule, "cursor: pointer;") {
		t.Fatalf("container rows should show click cursor; rule was: %s", itemRule)
	}
}

func TestContainerDetailExpandsInsideSelectedContainerRow(t *testing.T) {
	appData, err := Assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(appData)
	for _, want := range []string{"container-entry", "container-detail", "toggleContainer(item)", "renderContainerDetail(item)", "entry.appendChild(renderContainerDetail(item))", "openContainerProject(item)"} {
		if !strings.Contains(js, want) {
			t.Fatalf("inline container detail missing %q", want)
		}
	}
}

func TestContainerRowTogglesItsOwnDetail(t *testing.T) {
	appData, err := Assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(appData)
	start := strings.Index(js, "function renderContainers()")
	end := strings.Index(js, "function openContainerProject")
	if start == -1 || end == -1 || end <= start {
		t.Fatal("could not find renderContainers section")
	}
	renderContainers := js[start:end]
	for _, want := range []string{"btn.addEventListener('click', () => toggleContainer(item));", "event.preventDefault();", "toggleContainer(item);"} {
		if !strings.Contains(renderContainers, want) {
			t.Fatalf("container row should toggle its own detail; missing %q", want)
		}
	}
	for _, forbidden := range []string{"data-details", "openContainerProject(item)"} {
		if strings.Contains(renderContainers, forbidden) {
			t.Fatalf("container row should not navigate to the project or need a separate detail button; found %q", forbidden)
		}
	}
}

func TestContainerRowHidesDeleteUntilExpanded(t *testing.T) {
	appData, err := Assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(appData)
	start := strings.Index(js, "function renderContainers()")
	end := strings.Index(js, "function openContainerProject")
	if start == -1 || end == -1 || end <= start {
		t.Fatal("could not find renderContainers section")
	}
	renderContainers := js[start:end]
	for _, forbidden := range []string{"data-delete", "deleteContainer(item)"} {
		if strings.Contains(renderContainers, forbidden) {
			t.Fatalf("container row should not expose delete before expansion; found %q in %s", forbidden, renderContainers)
		}
	}
}

func TestContainerDetailShowsControlButtons(t *testing.T) {
	appData, err := Assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(appData)
	for _, want := range []string{"data-start", "data-stop", "data-restart", "data-logs", "containerLifecycle(item, 'start')", "containerLifecycle(item, 'stop')", "containerLifecycle(item, 'restart')", "containerLogs(item)", "/api/containers/${encodeURIComponent(id)}/actions/${action}", "/api/containers/${encodeURIComponent(id)}/logs"} {
		if !strings.Contains(js, want) {
			t.Fatalf("container detail controls missing %q", want)
		}
	}
}

func TestContainerDetailCanCollapseOnSecondClick(t *testing.T) {
	appData, err := Assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(appData)
	for _, want := range []string{"function toggleContainer(item)", "if (state.selectedContainer === item.id)", "state.selectedContainer = null", "renderContainers()"} {
		if !strings.Contains(js, want) {
			t.Fatalf("container detail collapse behavior missing %q", want)
		}
	}
}

func TestThemeAssetsSupportLightAndDarkModes(t *testing.T) {
	cssData, err := Assets.ReadFile("static/styles.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(cssData)
	for _, want := range []string{`:root[data-theme="light"]`, `:root[data-theme="dark"]`, `color-scheme: dark`, `--button-bg`, `--input-bg`, `--pre-bg`} {
		if !strings.Contains(css, want) {
			t.Fatalf("styles.css missing theme token %q", want)
		}
	}

	jsData, err := Assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(jsData)
	for _, want := range []string{"dockertree.theme", "themeToggle", "setTheme", "data-theme"} {
		if !strings.Contains(js, want) {
			t.Fatalf("app.js missing theme behavior %q", want)
		}
	}
}

func TestDeployUIAssetsExposeBothDeploymentModes(t *testing.T) {
	indexData, err := Assets.ReadFile("static/index.html")
	if err != nil {
		t.Fatal(err)
	}
	appData, err := Assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	combined := string(indexData) + string(appData)
	for _, want := range []string{"deployMode", "镜像部署", "Compose 部署", "/api/images/search", "/api/deploy/container", "/api/deploy/compose", "composePath", "composeContent"} {
		if !strings.Contains(combined, want) {
			t.Fatalf("deploy UI missing %q", want)
		}
	}
}

func TestProjectDirectoryAndComposeEditingWorkflowAssets(t *testing.T) {
	indexData, err := Assets.ReadFile("static/index.html")
	if err != nil {
		t.Fatal(err)
	}
	appData, err := Assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	combined := string(indexData) + string(appData)
	for _, want := range []string{
		"projectRoot", "scanPaths", "saveProjectSettings", "/api/settings/projects",
		"composeFiles", "editComposeFile", "/compose?path=", "composeSave",
		"/api/deploy/compose/save", "syncComposePath", "仅保存", "保存并部署",
	} {
		if !strings.Contains(combined, want) {
			t.Fatalf("project directory and compose editing workflow missing %q", want)
		}
	}
	if !strings.Contains(string(indexData), `id="composePath"`) || !strings.Contains(string(indexData), "readonly") {
		t.Fatal("derived compose path must be displayed as a read-only field")
	}
}

func TestProjectComposeEditingStaysInDetailModalAndOnlySaves(t *testing.T) {
	appData, err := Assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	cssData, err := Assets.ReadFile("static/styles.css")
	if err != nil {
		t.Fatal(err)
	}
	js := string(appData)
	start := strings.Index(js, "async function editComposeFile(project, composePath)")
	end := strings.Index(js[start:], "\nfunction ensureProjectVisible")
	if start == -1 || end == -1 {
		t.Fatal("could not locate project Compose editor workflow")
	}
	workflow := js[start : start+end]
	for _, want := range []string{"showModal()", "saveProjectCompose", "/api/deploy/compose/save", "dialog.close()"} {
		if !strings.Contains(workflow, want) {
			t.Fatalf("project Compose modal workflow missing %q: %s", want, workflow)
		}
	}
	for _, unwanted := range []string{"setActiveView('deploy')", "setDeployMode('compose')", "/api/deploy/compose\""} {
		if strings.Contains(workflow, unwanted) {
			t.Fatalf("project Compose editor should not deploy or leave detail; found %q", unwanted)
		}
	}
	if !strings.Contains(string(cssData), ".compose-editor-dialog") {
		t.Fatal("Compose editor dialog styles are missing")
	}
}

func TestComposeEditorsSupportFormattingAndKeyboardIndentation(t *testing.T) {
	indexData, err := Assets.ReadFile("static/index.html")
	if err != nil {
		t.Fatal(err)
	}
	appData, err := Assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	cssData, err := Assets.ReadFile("static/styles.css")
	if err != nil {
		t.Fatal(err)
	}
	combined := string(indexData) + string(appData)
	for _, want := range []string{
		`id="composeContent" class="compose-editor"`,
		`id="composeFormat"`,
		`data-compose-editor-content class="compose-editor"`,
		`data-compose-format`,
		`/api/deploy/compose/format`,
		"setupComposeEditor",
		"handleComposeEditorKeydown",
		"event.key === 'Tab'",
		"event.shiftKey",
		"setRangeText",
		"event.key === 'Enter'",
		"textarea.dispatchEvent(new Event('input', { bubbles: true }))",
	} {
		if !strings.Contains(combined, want) {
			t.Fatalf("Compose editor formatting or indentation support missing %q", want)
		}
	}
	if !strings.Contains(string(cssData), ".compose-editor") || !strings.Contains(string(cssData), "tab-size: 2") {
		t.Fatal("Compose editor code formatting styles are missing")
	}
}

func TestComposeDeployUsesSyntaxHighlightedDialogEditor(t *testing.T) {
	indexData, err := Assets.ReadFile("static/index.html")
	if err != nil {
		t.Fatal(err)
	}
	appData, err := Assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	cssData, err := Assets.ReadFile("static/styles.css")
	if err != nil {
		t.Fatal(err)
	}
	html := string(indexData)
	js := string(appData)
	css := string(cssData)

	formStart := strings.Index(html, `id="composeDeployForm"`)
	if formStart == -1 {
		t.Fatal("Compose deploy form is missing")
	}
	formEndOffset := strings.Index(html[formStart:], "</form>")
	if formEndOffset == -1 {
		t.Fatal("Compose deploy form is not closed")
	}
	form := html[formStart : formStart+formEndOffset]
	for _, want := range []string{`id="openComposeEditor"`, `id="composeContentSummary"`, "编辑 Compose"} {
		if !strings.Contains(form, want) {
			t.Fatalf("Compose deploy form editor launcher missing %q", want)
		}
	}
	if strings.Contains(form, `id="composeContent"`) {
		t.Fatal("Compose deploy form must open the editor dialog instead of showing an inline textarea")
	}

	for _, want := range []string{
		`id="composeEditorDialog"`, `data-compose-code-editor`, `data-compose-highlight`,
		`id="composeContent" class="compose-editor"`, `id="composeFormat"`, `id="composeEditorDone"`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("Compose deploy editor dialog missing %q", want)
		}
	}
	for _, want := range []string{
		"showComposeDeployEditor", "composeEditorDialog.showModal()", "syncComposeHighlight",
		"highlightComposeYAML", "highlightYAMLValue", "findYAMLCommentStart", "escapeHTML",
		"data-compose-highlight", "composeContentSummary", "composeContentEditor.value.trimEnd().split('\\n').length",
	} {
		if !strings.Contains(js, want) {
			t.Fatalf("Compose syntax highlighting behavior missing %q", want)
		}
	}
	for _, want := range []string{
		".compose-code-editor", ".compose-highlight", ".yaml-key", ".yaml-string",
		".yaml-number", ".yaml-keyword", ".yaml-comment", "color: transparent", "caret-color:",
		".compose-editor-launch {\n    grid-template-columns: minmax(0, 1fr);",
	} {
		if !strings.Contains(css, want) {
			t.Fatalf("Compose syntax highlighting styles missing %q", want)
		}
	}
}

func TestProjectDetailSeparatesUpdateAndRedeployActions(t *testing.T) {
	appData, err := Assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(appData)
	for _, want := range []string{
		`id="deploy">更新`, `id="redeploy">重新部署`, "async function deploy(id)", "async function redeploy(id)",
		"/actions/deploy", "/actions/redeploy", "startProjectLogRefresh(id)",
	} {
		if !strings.Contains(js, want) {
			t.Fatalf("project update/redeploy UI missing %q", want)
		}
	}
	start := strings.Index(js, "async function redeploy(id)")
	end := strings.Index(js[start:], "\nasync function deleteContainer")
	if start == -1 || end == -1 {
		t.Fatal("could not locate project redeploy workflow")
	}
	workflow := js[start : start+end]
	refresh := strings.Index(workflow, "startProjectLogRefresh(id)")
	request := strings.Index(workflow, "/actions/redeploy")
	if refresh == -1 || request == -1 || refresh > request {
		t.Fatalf("live logs must start before redeploy request: %s", workflow)
	}
}

func TestContainerDetailSupportsIndividualUpdateCheckAndRedeploy(t *testing.T) {
	appData, err := Assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(appData)
	for _, want := range []string{
		"data-check-update", "检查更新", "data-update", ">更新</button>", "data-redeploy", ">重新部署</button>",
		"containerCheckUpdate", "/actions/check-update", "containerDeploy", "/actions/deploy", "containerRedeploy", "/actions/redeploy",
	} {
		if !strings.Contains(js, want) {
			t.Fatalf("container update UI missing %q", want)
		}
	}
}

func TestComposeEditorProtectsExistingProjectsAndPendingChanges(t *testing.T) {
	indexData, err := Assets.ReadFile("static/index.html")
	if err != nil {
		t.Fatal(err)
	}
	appData, err := Assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	html := string(indexData)
	js := string(appData)
	for _, want := range []string{
		"composeEditingProjectId", "projectId: state.composeEditingProjectId",
		"composeDirty", "confirmDiscardComposeChanges", "beforeunload",
		"projectSettingsReady", "setProjectSettingsEnabled",
		"scanPromise", "queueAfterCurrent", "scanInventory({ queueAfterCurrent: true })",
	} {
		if !strings.Contains(js, want) {
			t.Fatalf("safe Compose workflow missing %q", want)
		}
	}
	for _, want := range []string{
		`id="projectRoot" value="/opt" placeholder="/opt" disabled`,
		`id="scanPaths" class="compact-textarea" spellcheck="false" placeholder="/opt" disabled`,
		`id="saveProjectSettings" type="button" disabled`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("project settings loading guard missing %q", want)
		}
	}
}

func TestLocalImagesUIAssets(t *testing.T) {
	indexData, err := Assets.ReadFile("static/index.html")
	if err != nil {
		t.Fatal(err)
	}
	appData, err := Assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	combined := string(indexData) + string(appData)
	for _, want := range []string{"本地镜像", "localImages", "refreshLocalImages", "/api/images/local"} {
		if !strings.Contains(combined, want) {
			t.Fatalf("local images UI missing %q", want)
		}
	}
}

func TestImageDeployUIUsesSingleSearchInputWithAdvancedName(t *testing.T) {
	indexData, err := Assets.ReadFile("static/index.html")
	if err != nil {
		t.Fatal(err)
	}
	appData, err := Assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	combined := string(indexData) + string(appData)
	for _, want := range []string{"imageQuery", "高级选项", "advancedContainerOptions", "deriveContainerName"} {
		if !strings.Contains(combined, want) {
			t.Fatalf("single-search image deploy UI missing %q", want)
		}
	}
	if strings.Contains(string(indexData), "镜像名称<input") || strings.Contains(string(indexData), "容器名称<input") {
		t.Fatal("image deploy should not show image and container names as two peer required fields")
	}
}

func TestDeleteUIAssets(t *testing.T) {
	indexData, err := Assets.ReadFile("static/index.html")
	if err != nil {
		t.Fatal(err)
	}
	appData, err := Assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	combined := string(indexData) + string(appData)
	for _, want := range []string{"删除", "deleteContainer", "deleteProject", "deleteImage", "确认删除", "operationOutput", "showOperationResult"} {
		if !strings.Contains(combined, want) {
			t.Fatalf("delete UI missing %q", want)
		}
	}
}

func TestProjectDetailUsesContainerDeleteForStandaloneProjects(t *testing.T) {
	appData, err := Assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(appData)
	for _, want := range []string{"isStandaloneProject", "standaloneDeleteTarget", "删除容器", "deleteContainer(standaloneDeleteTarget(project))"} {
		if !strings.Contains(js, want) {
			t.Fatalf("standalone project delete UI missing %q", want)
		}
	}
}

func TestTokenSaveLoadsProjectsAndLocalImages(t *testing.T) {
	appData, err := Assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(appData)
	start := strings.Index(js, "document.querySelector('#saveToken').addEventListener('click'")
	end := strings.Index(js, "document.querySelector('#scan').addEventListener('click'")
	if start == -1 || end == -1 || end <= start {
		t.Fatal("could not find save token listener")
	}
	listener := js[start:end]
	for _, want := range []string{"loadProjects()", "loadLocalImages()"} {
		if !strings.Contains(listener, want) {
			t.Fatalf("save token listener should call %q: %s", want, listener)
		}
	}
}

func TestScanErrorsAreShownInGlobalOperationOutput(t *testing.T) {
	appData, err := Assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(appData)
	start := strings.Index(js, "document.querySelector('#scan').addEventListener('click'")
	end := strings.Index(js, "for (const button of document.querySelectorAll('#viewTabs")
	if start == -1 || end == -1 || end <= start {
		t.Fatal("could not find scan listener")
	}
	listener := js[start:end]
	for _, want := range []string{"try", "catch", "showOperationResult({ error: err.message })"} {
		if !strings.Contains(listener, want) {
			t.Fatalf("scan listener should surface errors with %q: %s", want, listener)
		}
	}
}

func TestProjectSelectionIsReconciledAfterReload(t *testing.T) {
	appData, err := Assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(appData)
	for _, want := range []string{"reconcileSelectedProject", "state.selected = null", "state.selectedContainer = null"} {
		if !strings.Contains(js, want) {
			t.Fatalf("app should reconcile stale selections after reload; missing %q", want)
		}
	}
}

func TestLifecycleUIRefreshesProjectsAfterAction(t *testing.T) {
	appData, err := Assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(appData)
	start := strings.Index(js, "async function lifecycle")
	end := strings.Index(js, "async function logs")
	if start == -1 || end == -1 || end <= start {
		t.Fatal("could not find lifecycle function")
	}
	fn := js[start:end]
	if !strings.Contains(fn, "await loadProjects()") {
		t.Fatalf("lifecycle action should refresh projects after success: %s", fn)
	}
}

func TestDeployResultFormattingIncludesErrorDetails(t *testing.T) {
	appData, err := Assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(appData)
	for _, want := range []string{"result.error", "result.exitCode", "result.output"} {
		if !strings.Contains(js, want) {
			t.Fatalf("deploy result formatting should include %q", want)
		}
	}
}

func TestDeployResultFormattingIncludesPlanWarnings(t *testing.T) {
	appData, err := Assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(appData)
	for _, want := range []string{"result.warnings", "Warnings:", "...result.warnings.map"} {
		if !strings.Contains(js, want) {
			t.Fatalf("deploy result formatting should include plan warnings; missing %q", want)
		}
	}
}

func TestImageDeleteKeepsCommandResultVisible(t *testing.T) {
	appData, err := Assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(appData)
	for _, want := range []string{"async function loadLocalImages(options = {})", "if (!options.silent)", "if (result.error) return", "loadLocalImages({ silent: true })"} {
		if !strings.Contains(js, want) {
			t.Fatalf("image delete should preserve command output; missing %q", want)
		}
	}
}

func TestImageDeleteOffersForceRetryOnDockerConflict(t *testing.T) {
	appData, err := Assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(appData)
	for _, want := range []string{"requiresForceImageDelete", "force: true", "强制删除镜像", "deleteImage(ref, true)"} {
		if !strings.Contains(js, want) {
			t.Fatalf("image delete should offer force retry on Docker conflict; missing %q", want)
		}
	}
}

func TestOperationsDashboardAssets(t *testing.T) {
	indexData, err := Assets.ReadFile("static/index.html")
	if err != nil {
		t.Fatal(err)
	}
	appData, err := Assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	combined := string(indexData) + string(appData)
	for _, want := range []string{
		`data-view="overview"`, "overviewView", "概览", "需要关注", "资源快照",
		"summaryProjects", "summaryContainers", "summaryImages", "summaryRunning", "summaryStopped", "summaryUnhealthy",
		"/api/containers/stats", "renderOverview", "loadStats",
	} {
		if !strings.Contains(combined, want) {
			t.Fatalf("operations dashboard missing %q", want)
		}
	}
}

func TestSearchFilterAndMetadataAssets(t *testing.T) {
	indexData, err := Assets.ReadFile("static/index.html")
	if err != nil {
		t.Fatal(err)
	}
	appData, err := Assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	combined := string(indexData) + string(appData)
	for _, want := range []string{
		"globalSearch", "statusFilter", "healthFilter", "typeFilter", "tagFilter", "favoriteOnly", "sortBy",
		"filteredProjects", "filteredContainers", "project.favorite", "project.tags", "project.aliases",
		"saveProjectMetadata", "/api/projects/${encodeURIComponent(project.id)}/metadata", "method: 'PATCH'",
	} {
		if !strings.Contains(combined, want) {
			t.Fatalf("search/filter/metadata UI missing %q", want)
		}
	}
}

func TestEnhancedLogsAndAutoRefreshAssets(t *testing.T) {
	indexData, err := Assets.ReadFile("static/index.html")
	if err != nil {
		t.Fatal(err)
	}
	appData, err := Assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	combined := string(indexData) + string(appData)
	for _, want := range []string{
		"logTail", "logTimestamps", "logKeyword", "logService", "refreshLogs", "filterLogText",
		"dockertree.autoRefreshInterval", "autoRefreshInterval", "syncAutoRefresh", "document.hidden", "visibilitychange",
	} {
		if !strings.Contains(combined, want) {
			t.Fatalf("enhanced logs/auto refresh UI missing %q", want)
		}
	}
}

func TestHistoryAndSafeInspectAssets(t *testing.T) {
	indexData, err := Assets.ReadFile("static/index.html")
	if err != nil {
		t.Fatal(err)
	}
	appData, err := Assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	combined := string(indexData) + string(appData)
	for _, want := range []string{
		`data-view="history"`, "historyView", "操作历史", "historyFailedOnly", "historyTarget", "/api/operations",
		"containerInspect", "/api/containers/${encodeURIComponent(id)}/inspect", "restartPolicy", "inspectMounts",
		"projectLinks", "addServiceLink", "collectServiceLinks",
	} {
		if !strings.Contains(combined, want) {
			t.Fatalf("history/inspect UI missing %q", want)
		}
	}
}

func TestMaintenanceAutomationAssets(t *testing.T) {
	indexData, err := Assets.ReadFile("static/index.html")
	if err != nil {
		t.Fatal(err)
	}
	appData, err := Assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	combined := string(indexData) + string(appData)
	for _, want := range []string{
		`data-view="maintenance"`, "maintenanceView", "维护", "checkAllUpdates", "/api/updates/check",
		"updateCheckInterval", "webhookURL", "webhookType", "/api/settings/automation", "/api/notifications/test",
		"cleanupPreview", "/api/cleanup/preview", "executeCleanup", "确认执行所选清理",
		"exportConfig", "/api/config/export", "restoreConfig", "/api/config/restore",
	} {
		if !strings.Contains(combined, want) {
			t.Fatalf("maintenance UI missing %q", want)
		}
	}
}

func TestMaintenanceUpdateRowsShowVersionsAndOneClickDeploy(t *testing.T) {
	appData, err := Assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	cssData, err := Assets.ReadFile("static/styles.css")
	if err != nil {
		t.Fatal(err)
	}
	js := string(appData)
	for _, want := range []string{
		"renderUpdateVersions", "version.current", "version.available", "data-update-versions",
		"data-deploy-update", "一键更新并部署", "deployCheckedProject",
		"/api/projects/${encodeURIComponent(check.projectId)}/actions/deploy",
	} {
		if !strings.Contains(js, want) {
			t.Fatalf("maintenance update row missing %q", want)
		}
	}
	if !strings.Contains(string(cssData), ".update-check-versions") || !strings.Contains(string(cssData), ".update-check-actions") {
		t.Fatal("maintenance update row layout styles are missing")
	}
}

func TestAdvancedDeployComposeDiffAndTemplateAssets(t *testing.T) {
	indexData, err := Assets.ReadFile("static/index.html")
	if err != nil {
		t.Fatal(err)
	}
	appData, err := Assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	combined := string(indexData) + string(appData)
	for _, want := range []string{
		"containerPorts", "containerEnv", "containerVolumes", "containerNetwork", "containerRestartPolicy",
		"templateSelect", "saveTemplate", "deleteTemplate", "/api/templates",
		"composeDiff", "existingContent", "normalizedContent", "existingHash", "expectedExistingHash",
	} {
		if !strings.Contains(combined, want) {
			t.Fatalf("advanced deploy UI missing %q", want)
		}
	}
}

func TestMobileDeployLayoutConstrainsGridContent(t *testing.T) {
	cssData, err := Assets.ReadFile("static/styles.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(cssData)
	for _, want := range []string{
		".deploy-body {\n  display: grid;\n  grid-template-columns: minmax(0, 1fr);",
		".deploy-body > * {\n  min-width: 0;",
		".compose-diff > * {\n  min-width: 0;",
		".compose-diff {\n    grid-template-columns: minmax(0, 1fr);",
	} {
		if !strings.Contains(css, want) {
			t.Fatalf("deploy layout must prevent long preview content from widening the page; missing %q", want)
		}
	}
}

func TestMobileImageLayoutConstrainsLongImageNames(t *testing.T) {
	cssData, err := Assets.ReadFile("static/styles.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(cssData)
	for _, want := range []string{
		".image-list {\n  display: grid;\n  grid-template-columns: minmax(0, 1fr);",
		".field-grid,\n  .search-result,\n  .image-row {\n    grid-template-columns: minmax(0, 1fr);",
		".search-result > *,\n.image-row > * {\n  min-width: 0;",
		".search-result strong,\n.image-row strong {\n  overflow-wrap: anywhere;",
	} {
		if !strings.Contains(css, want) {
			t.Fatalf("image layout must allow long image names to shrink on mobile; missing %q", want)
		}
	}
}

func TestOperationRequestsRenderStreamingOutputInRealTime(t *testing.T) {
	indexData, err := Assets.ReadFile("static/index.html")
	if err != nil {
		t.Fatal(err)
	}
	appData, err := Assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	html := string(indexData)
	js := string(appData)
	for _, want := range []string{`<progress id="operationProgressBar"`, `id="operationProgressSummary"`, `id="operationProgressDetail"`} {
		if !strings.Contains(html, want) {
			t.Fatalf("native operation progress UI missing %q", want)
		}
	}
	for _, want := range []string{
		"application/x-ndjson",
		"res.body.getReader()",
		"TextDecoder",
		"readOperationStream",
		"appendOperationOutput",
		"method !== 'GET'",
		"event.type === 'command'",
		"event.type === 'output'",
		"event.type === 'progress'",
		"updateOperationProgress(event.progress)",
		"operationProgressBar.value",
		"operationOutput.scrollTop = operationOutput.scrollHeight",
	} {
		if !strings.Contains(js, want) {
			t.Fatalf("real-time operation output missing %q", want)
		}
	}
}

func TestDeployActionsUseCompactProgressDisplay(t *testing.T) {
	appData, err := Assets.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(appData)
	for _, want := range []string{
		"function deploymentRequestOptions(payload)",
		"operationLabel: '部署进度'",
		"compactOutput: true",
		"previewOnly ? jsonPost(payload) : deploymentRequestOptions(payload)",
		"saveOnly ? jsonPost(payload) : deploymentRequestOptions(payload)",
		"if (display.compactOutput)",
		"setOperationProgressDetail(event.data)",
	} {
		if !strings.Contains(js, want) {
			t.Fatalf("deploy progress display missing %q", want)
		}
	}
}
