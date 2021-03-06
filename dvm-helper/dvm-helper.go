package main

import "errors"
import "fmt"
import "io/ioutil"
import "os"
import "os/exec"
import "path/filepath"
import "regexp"
import "sort"
import "strings"
import "github.com/blang/semver"
import "github.com/fatih/color"
import "github.com/getcarina/dvm/dvm-helper/url"
import "github.com/google/go-github/github"
import "github.com/codegangsta/cli"
import "github.com/kardianos/osext"
import "github.com/ryanuber/go-glob"
import "golang.org/x/oauth2"

// These are global command line variables
var shell string
var dvmDir string
var debug bool
var silent bool
var token string

// These are set during the build
var dvmVersion string
var dvmCommit string

const (
	retCodeInvalidArgument  = 127
	retCodeInvalidOperation = 3
	retCodeRuntimeError     = 1
)

func main() {
	app := cli.NewApp()
	app.Name = "Docker Version Manager"
	app.Usage = "Manage multiple versions of the Docker client"
	app.Version = fmt.Sprintf("%s (%s)", dvmVersion, dvmCommit)
	app.EnableBashCompletion = true
	app.Flags = []cli.Flag{
		cli.StringFlag{Name: "github-token", EnvVar: "GITHUB_TOKEN", Usage: "Increase the github api rate limit by specifying your github personal access token."},
		cli.StringFlag{Name: "dvm-dir", EnvVar: "DVM_DIR", Usage: "Specify an alternate DVM home directory, defaults to the current directory."},
		cli.StringFlag{Name: "shell", EnvVar: "SHELL", Usage: "Specify the shell format in which environment variables should be output, e.g. powershell, cmd or sh/bash. Defaults to sh/bash."},
		cli.BoolFlag{Name: "debug", Usage: "Print additional debug information."},
		cli.BoolFlag{Name: "silent", EnvVar: "DVM_SILENT", Usage: "Suppress output. Errors will still be displayed."},
	}
	app.Commands = []cli.Command{
		{
			Name:    "install",
			Aliases: []string{"i"},
			Usage:   "dvm install [<version>], dvm install experimental\n\tInstall a Docker version, using $DOCKER_VERSION if the version is not specified.",
			Action: func(c *cli.Context) {
				setGlobalVars(c)
				install(c.Args().First())
			},
		},
		{
			Name:  "uninstall",
			Usage: "dvm uninstall <version>\n\tUninstall a Docker version.",
			Action: func(c *cli.Context) {
				setGlobalVars(c)
				uninstall(c.Args().First())
			},
		},
		{
			Name:  "use",
			Usage: "dvm use [<version>], dvm use system, dvm use experimental\n\tUse a Docker version, using $DOCKER_VERSION if the version is not specified.",
			Action: func(c *cli.Context) {
				setGlobalVars(c)
				use(c.Args().First())
			},
		},
		{
			Name:  "deactivate",
			Usage: "dvm deactivate\n\tUndo the effects of `dvm` on current shell.",
			Action: func(c *cli.Context) {
				setGlobalVars(c)
				deactivate()
			},
		},
		{
			Name:  "current",
			Usage: "dvm current\n\tPrint the current Docker version.",
			Action: func(c *cli.Context) {
				setGlobalVars(c)
				current()
			},
		},
		{
			Name:  "which",
			Usage: "dvm which\n\tPrint the path to the current Docker version.",
			Action: func(c *cli.Context) {
				setGlobalVars(c)
				which()
			},
		},
		{
			Name:  "alias",
			Usage: "dvm alias <alias> <version>\n\tCreate an alias to a Docker version.",
			Action: func(c *cli.Context) {
				setGlobalVars(c)
				alias(c.Args().Get(0), c.Args().Get(1))
			},
		},
		{
			Name:  "unalias",
			Usage: "dvm unalias <alias>\n\tRemove a Docker version alias.",
			Action: func(c *cli.Context) {
				setGlobalVars(c)
				unalias(c.Args().First())
			},
		},
		{
			Name:    "list",
			Aliases: []string{"ls"},
			Usage:   "dvm list [<pattern>]\n\tList installed Docker versions.",
			Action: func(c *cli.Context) {
				setGlobalVars(c)
				list(c.Args().First())
			},
		},
		{
			Name:    "list-remote",
			Aliases: []string{"ls-remote"},
			Usage:   "dvm list-remote [<pattern>]\n\tList available Docker versions.",
			Action: func(c *cli.Context) {
				setGlobalVars(c)
				listRemote(c.Args().First())
			},
		},
		{
			Name:    "list-alias",
			Aliases: []string{"ls-alias"},
			Usage:   "dvm list-alias\n\tList Docker version aliases.",
			Action: func(c *cli.Context) {
				setGlobalVars(c)
				listAlias()
			},
		},
		{
			Name:  "upgrade",
			Usage: "dvm upgrade\n\tUpgrade dvm to the latest release.",
			Flags: []cli.Flag{
				cli.BoolFlag{Name: "check", Usage: "Checks if an newer version of dvm is available, but does not perform the upgrade."},
				cli.StringFlag{Name: "version", Usage: "Upgrade to the specified version."},
			},
			Action: func(c *cli.Context) {
				setGlobalVars(c)
				upgrade(c.Bool("check"), c.String("version"))
			},
		},
	}

	app.Run(os.Args)
}

func setGlobalVars(c *cli.Context) {
	debug = c.GlobalBool("debug")
	token = c.GlobalString("github-token")
	shell = c.GlobalString("shell")
	validateShellFlag()

	silent = c.GlobalBool("silent")
	dvmDir = c.GlobalString("dvm-dir")
	if dvmDir == "" {
		var err error
		dvmDir, err = osext.ExecutableFolder()
		if err != nil {
			die("Unable to determine DVM home directory", nil, 1)
		}
	}
}

func upgrade(checkOnly bool, version string) {
	if version != "" && dvmVersion == version {
		writeWarning("dvm %s is already installed.", version)
		return
	}

	if version == "" {
		shouldUpgrade, latestVersion := isUpgradeAvailable()
		if !shouldUpgrade {
			writeInfo("The latest version of dvm is already installed.")
			return
		}

		version = latestVersion
	}

	if checkOnly {
		writeInfo("dvm %s is available. Run `dvm upgrade` to install the latest version.", version)
		return
	}

	writeInfo("Upgrading to dvm %s...", version)
	upgradeSelf(version)
}

func buildDvmReleaseURL(version string, elem ...string) string {
	prefix := url.Join("https://download.getcarina.com/dvm", version)
	suffix := url.Join(elem...)
	return url.Join(prefix, suffix)
}

func current() {
	current, err := getCurrentDockerVersion()
	if err != nil {
		writeWarning("N/A")
	} else {
		writeInfo(current)
	}
}

func list(pattern string) {
	pattern += "*"
	versions := getInstalledVersions(pattern)
	current, _ := getCurrentDockerVersion()

	for _, version := range versions {
		if current == version {
			color.Green("->\t%s", version)
		} else {
			writeInfo("\t%s", version)
		}
	}
}

func install(version string) {
	if version == "" {
		version = getDockerVersionVar()
	}

	if version == "" {
		die("The install command requires that a version is specified or the DOCKER_VERSION environment variable is set.", nil, retCodeInvalidArgument)
	}

	if !versionExists(version) {
		die("Version %s not found - try `dvm ls-remote` to browse available versions.", nil, retCodeInvalidOperation, version)
	}

	versionDir := getVersionDir(version)

	if version == "experimental" && pathExists(versionDir) {
		// Always install latest of experimental build
		err := os.RemoveAll(versionDir)
		if err != nil {
			die("Unable to remove experimental version at %s.", err, retCodeRuntimeError, versionDir)
		}
	}

	if _, err := os.Stat(versionDir); err == nil {
		writeWarning("%s is already installed", version)
		use(version)
		return
	}

	writeInfo("Installing %s...", version)

	url := buildDownloadURL(version)
	binaryPath := filepath.Join(getDvmDir(), "bin/docker", version, getBinaryName())
	downloadFileWithChecksum(url, binaryPath)

	writeDebug("Installed Docker %s to %s.", version, binaryPath)
	use(version)
}

func buildDownloadURL(version string) string {
	mirrorURL := "https://get.docker.com/builds"

	if version == "experimental" {
		mirrorURL = "https://experimental.docker.com/builds"
		version = "latest"
	}

	return fmt.Sprintf("%s/%s/%s/docker-%s%s", mirrorURL, dockerOS, dockerArch, version, binaryFileExt)
}

func uninstall(version string) {
	if version == "" {
		die("The uninstall command requires that a version is specified.", nil, retCodeInvalidArgument)
	}

	current, _ := getCurrentDockerVersion()
	if current == version {
		die("Cannot uninstall the currently active Docker version.", nil, retCodeInvalidOperation)
	}

	versionDir := getVersionDir(version)
	if _, err := os.Stat(versionDir); os.IsNotExist(err) {
		writeWarning("%s is not installed.", version)
		return
	}

	err := os.RemoveAll(versionDir)
	if err != nil {
		die("Unable to uninstall Docker version %s located in %s.", err, retCodeRuntimeError, version, versionDir)
	}

	writeInfo("Uninstalled Docker %s.", version)
}

func use(version string) {
	if version == "" {
		version = getDockerVersionVar()
	}

	if version == "" {
		die("The use command requires that a version is specified or the DOCKER_VERSION environment variable is set.", nil, retCodeInvalidOperation)
	}

	// dvm use system undoes changes to the PATH and uses installed version of DOcker
	if version == "system" {
		systemDockerVersion, err := getSystemDockerVersion()
		if err != nil {
			die("System version of Docker not found.", nil, retCodeInvalidOperation)
		}

		removePreviousDvmVersionFromPath()
		writeInfo("Now using system version of Docker: %s", systemDockerVersion)
		writePathScript()
		return
	}

	if aliasExists(version) {
		alias := version
		aliasedVersion, _ := ioutil.ReadFile(getAliasPath(alias))
		version = string(aliasedVersion)
		writeDebug("Using alias: %s -> %s", alias, version)
	}

	ensureVersionIsInstalled(version)
	removePreviousDvmVersionFromPath()
	prependDvmVersionToPath(version)
	writePathScript()

	writeInfo("Now using Docker %s", version)
}

func which() {
	currentPath, err := getCurrentDockerPath()
	if err == nil {
		writeInfo(currentPath)
	}
}

func alias(alias string, version string) {
	if alias == "" || version == "" {
		die("The alias command requires both an alias name and a version.", nil, retCodeInvalidArgument)
	}

	if !isVersionInstalled(version) {
		die("The aliased version, %s, is not installed.", nil, retCodeInvalidArgument, version)
	}

	aliasPath := getAliasPath(alias)
	if _, err := os.Stat(aliasPath); err == nil {
		writeDebug("Overwriting existing alias.")
	}

	writeFile(aliasPath, version)
	writeInfo("Aliased %s to %s.", alias, version)
}

func unalias(alias string) {
	if alias == "" {
		die("The unalias command requires an alias name.", nil, retCodeInvalidArgument)
	}

	if !aliasExists(alias) {
		writeWarning("%s is not an alias.", alias)
		return
	}

	aliasPath := getAliasPath(alias)
	err := os.Remove(aliasPath)
	if err != nil {
		die("Unable to remove alias %s at %s.", err, retCodeRuntimeError, alias, aliasPath)
	}

	writeInfo("Removed alias %s", alias)
}

func listAlias() {
	aliases := getAliases()
	for alias, version := range aliases {
		writeInfo("\t%s -> %s", alias, version)
	}
}

func aliasExists(alias string) bool {
	aliasPath := getAliasPath(alias)
	if _, err := os.Stat(aliasPath); err == nil {
		return true
	}

	return false
}

func getAliases() map[string]string {
	aliases, _ := filepath.Glob(getAliasPath("*"))

	results := make(map[string]string)
	for _, aliasPath := range aliases {
		alias := filepath.Base(aliasPath)
		version, err := ioutil.ReadFile(aliasPath)
		if err != nil {
			writeDebug("Excluding alias: %s.", err, retCodeRuntimeError, alias)
			continue
		}

		results[alias] = string(version)
	}

	return results
}

func getDvmDir() string {
	return dvmDir
}

func getAliasPath(alias string) string {
	return filepath.Join(dvmDir, "alias", alias)
}

func getDockerBinaryName(version string) string {
	if version == "experimental" {
		version = "latest"
	}

	return fmt.Sprintf("docker-%s%s", version, binaryFileExt)
}

func getBinaryName() string {
	return "docker" + binaryFileExt
}

func deactivate() {
	removePreviousDvmVersionFromPath()
	writePathScript()
}

func prependDvmVersionToPath(version string) {
	prependPath(getVersionDir(version))
}

func writePathScript() {
	// Write to a shell script for the calling wrapper to execute
	scriptPath := buildDvmOutputScriptPath()
	contents := buildPathScript()

	writeFile(scriptPath, contents)
}

func buildDvmOutputScriptPath() string {
	var fileExtension string
	if shell == "powershell" {
		fileExtension = "ps1"
	} else if shell == "cmd" {
		fileExtension = "cmd"
	} else { // default to bash
		fileExtension = "sh"
	}
	return filepath.Join(dvmDir, ".tmp", ("dvm-output." + fileExtension))
}

func removePreviousDvmVersionFromPath() {
	removePath(getCleanDvmPathRegex())
}

func ensureVersionIsInstalled(version string) {
	if isVersionInstalled(version) {
		return
	}

	writeInfo("%s is not installed. Installing now...", version)
	install(version)
}

func isVersionInstalled(version string) bool {
	installedVersions := getInstalledVersions(version)

	return len(installedVersions) > 0
}

func versionExists(version string) bool {
	if version == "experimental" {
		return true
	}

	availableVersions := getAvailableVersions(version)

	for _, availableVersion := range availableVersions {
		if version == availableVersion {
			return true
		}
	}
	return false
}

func getCurrentDockerPath() (string, error) {
	currentDockerPath, err := exec.LookPath("docker")
	return currentDockerPath, err
}

func getCurrentDockerVersion() (string, error) {
	currentDockerPath, err := getCurrentDockerPath()
	if err != nil {
		return "", err
	}
	current, _ := getDockerVersion(currentDockerPath)

	systemDockerPath, _ := getSystemDockerPath()
	if currentDockerPath == systemDockerPath {
		current = fmt.Sprintf("system (%s)", current)
	}

	experimentalVersionPath, _ := getExperimentalDockerPath()
	if currentDockerPath == experimentalVersionPath {
		current = fmt.Sprintf("experimental (%s)", current)
	}

	return current, nil
}

func getSystemDockerPath() (string, error) {
	originalPath := getPath()
	removePreviousDvmVersionFromPath()
	systemDockerPath, err := exec.LookPath("docker")
	setPath(originalPath)
	return systemDockerPath, err
}

func getSystemDockerVersion() (string, error) {
	systemDockerPath, err := getSystemDockerPath()
	if err != nil {
		return "", err
	}
	return getDockerVersion(systemDockerPath)
}

func getExperimentalDockerPath() (string, error) {
	experimentalVersionPath := filepath.Join(getVersionDir("experimental"), getBinaryName())
	_, err := os.Stat(experimentalVersionPath)
	return experimentalVersionPath, err
}

func getExperimentalDockerVersion() (string, error) {
	experimentalVersionPath, err := getExperimentalDockerPath()
	if err != nil {
		return "", err
	}
	return getDockerVersion(experimentalVersionPath)
}

func getDockerVersion(dockerPath string) (string, error) {
	rawVersion, _ := exec.Command(dockerPath, "-v").Output()

	writeDebug("%s -v output: %s", dockerPath, rawVersion)

	versionRegex := regexp.MustCompile(`^Docker version (.*),`)
	match := versionRegex.FindSubmatch(rawVersion)
	if len(match) < 2 {
		return "", errors.New("Could not detect docker version.")
	}

	return string(match[1][:]), nil
}

func listRemote(pattern string) {
	versions := getAvailableVersions(pattern)
	for _, version := range versions {
		writeInfo(version)
	}
}

func getInstalledVersions(pattern string) []string {
	versions, _ := filepath.Glob(getVersionDir(pattern))

	var results []string
	for _, versionDir := range versions {
		version := filepath.Base(versionDir)

		if version == "experimental" {
			experimentalVersion, err := getExperimentalDockerVersion()
			if err != nil {
				writeDebug("Unable to get version of installed experimental version at %s.\n%s", getVersionDir("experimental"), err)
				continue
			}
			version = fmt.Sprintf("experimental (%s)", experimentalVersion)
		}

		results = append(results, version)
	}

	if glob.Glob(pattern, "system") {
		systemVersion, err := getSystemDockerVersion()
		if err == nil {
			results = append(results, fmt.Sprintf("system (%s)", systemVersion))
		}
	}

	sort.Strings(results)
	return results
}

func getAvailableVersions(pattern string) []string {
	gh := buildGithubClient()
	tags, response, err := gh.Repositories.ListTags("docker", "docker", nil)
	if err != nil {
		warnWhenRateLimitExceeded(err, response)
		die("Unable to retrieve list of Docker tags from GitHub", err, retCodeRuntimeError)
	}
	if response.StatusCode != 200 {
		die("Unable to retrieve list of Docker tags from GitHub (Status %s).", nil, retCodeRuntimeError, response.StatusCode)
	}

	versionRegex := regexp.MustCompile(`^v([1-9]+\.\d+\.\d+)$`)
	patternRegex, err := regexp.Compile(pattern)
	if err != nil {
		die("Invalid pattern.", err, retCodeInvalidOperation)
	}

	var results []string
	for _, tag := range tags {
		version := *tag.Name
		match := versionRegex.FindStringSubmatch(version)
		if len(match) > 1 && patternRegex.MatchString(version) {
			results = append(results, match[1])
		}
	}

	sort.Strings(results)
	return results
}

func isUpgradeAvailable() (bool, string) {
	gh := buildGithubClient()
	release, response, err := gh.Repositories.GetLatestRelease("getcarina", "dvm")
	if err != nil {
		warnWhenRateLimitExceeded(err, response)
		writeWarning("Unable to query the latest dvm release from GitHub:")
		writeWarning("%s", err)
		return false, ""
	}
	if response.StatusCode != 200 {
		writeWarning("Unable to query the latest dvm release from GitHub (Status %s):", response.StatusCode)
		return false, ""
	}

	currentVersion, err := semver.Make(dvmVersion)
	if err != nil {
		writeWarning("Unable to parse the current dvm version as a semantic version!")
		writeWarning("%s", err)
		return false, ""
	}
	latestVersion, err := semver.Make(*release.TagName)
	if err != nil {
		writeWarning("Unable to parse the latest dvm version as a semantic version!")
		writeWarning("%s", err)
		return false, ""
	}

	return latestVersion.Compare(currentVersion) > 0, *release.TagName
}

func getVersionDir(version string) string {
	return filepath.Join(dvmDir, "bin", "docker", version)
}

func getDockerVersionVar() string {
	return strings.TrimSpace(os.Getenv("DOCKER_VERSION"))
}

func buildGithubClient() *github.Client {
	if token != "" {
		tokenSource := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
		httpClient := oauth2.NewClient(oauth2.NoContext, tokenSource)
		return github.NewClient(httpClient)
	}

	return github.NewClient(nil)
}

func warnWhenRateLimitExceeded(err error, response *github.Response) {
	if err == nil {
		return
	}

	if response.StatusCode == 403 {
		writeWarning("Your GitHub API rate limit has been exceeded. Set the GITHUB_TOKEN environment variable or use the --github-token parameter with your GitHub personal access token to authenticate and increase the rate limit.")
	}
}
