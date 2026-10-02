// Package xspawn starts programs with the semantics of npm cross-spawn 7.0.6.
//
// On Windows, a resolved .exe or .com runs directly. Anything else, including
// .cmd and .bat shims and a name that cannot be resolved, runs through
// cmd.exe (%ComSpec%) with cross-spawn's argument escaping. The search checks
// the working directory first and then PATH, honoring PATHEXT. PATHEXT is
// read from the process environment, matching which's behavior when
// cross-spawn calls it.
//
// A missing command still starts cmd.exe. VerifyENOENT turns that exit
// status of 1 into a NotFoundError, which is the error event cross-spawn
// emits. VerifyENOENTSync reports the same failure with the spawnSync
// syscall name.
//
// On other platforms Command is exec.CommandContext. Cross-spawn does not
// rewrite shebangs or ENOENT there.
package xspawn
