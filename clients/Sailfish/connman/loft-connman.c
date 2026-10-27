/*
 * ConnMan VPN client for loft. connman-vpnd starts this process with
 * --dev <tun> and Loft.* mapped to flags. We spawn loft-front onto that
 * tun (-tun NAME -tun-noexec), wait for STATUS state=on, then run
 * loft-script so ConnMan can apply IP / DNS / routes.
 *
 * POSIX only. No glib. SIGTERM / SIGINT go to loft-front.
 *
 * Why a separate process: ConnMan runs a VPN as a task it can start and
 * stop, and expects OpenVPN-style flags and a notify script. loft-front is
 * the same engine the desktop uses and knows nothing about ConnMan. This
 * small wrapper translates between the two. -tun-noexec tells loft-front to
 * use the tun as is, because ConnMan, not loft-front, sets its address and
 * routes.
 */

#define _POSIX_C_SOURCE 200809L

#include <errno.h>
#include <fcntl.h>
#include <signal.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/types.h>
#include <sys/wait.h>
#include <unistd.h>

/*
 * Packaged install paths. CMake passes the real ones with -D, because
 * they differ between the Sailfish and the Aurora package.
 */
#ifndef LOFT_FRONT
#define LOFT_FRONT "/usr/libexec/harbour-loft/loft-front"
#endif
#ifndef LOFT_SCRIPT
#define LOFT_SCRIPT "/usr/lib/connman/scripts/loft-script"
#endif

/* The running loft-front, so the signal handler can pass signals on. */
static pid_t child_pid;
/* Set by on_signal: a stop was requested, so a failed wait is not an error. */
static volatile sig_atomic_t got_signal;

/* Forwards a stop signal to loft-front; main then returns its exit code. */
static void on_signal(int sig)
{
	got_signal = sig;
	if (child_pid > 0)
		kill(child_pid, sig);
}

/* strdup that returns NULL for NULL and exits on out-of-memory, so callers
 * need no error path. */
static char *xstrdup(const char *s)
{
	char *p;

	if (!s)
		return NULL;
	p = strdup(s);
	if (!p) {
		perror("strdup");
		exit(2);
	}
	return p;
}

/* True when path is a non-empty name of an executable file. */
static int file_ok(const char *path)
{
	return path && path[0] && access(path, X_OK) == 0;
}

/* Directory of this executable, from /proc/self/exe. */
static char *self_dir(char *buf, size_t n)
{
	ssize_t k = readlink("/proc/self/exe", buf, n - 1);
	char *slash;

	if (k <= 0)
		return NULL;
	buf[k] = '\0';
	slash = strrchr(buf, '/');
	if (!slash)
		return NULL;
	*slash = '\0';
	return buf;
}

/* loft-front next to this binary, then the packaged path, then /usr/bin. */
static const char *find_front(void)
{
	static char path[512];
	char dir[512];

	if (self_dir(dir, sizeof(dir))) {
		snprintf(path, sizeof(path), "%s/loft-front", dir);
		if (file_ok(path))
			return path;
	}
	if (file_ok(LOFT_FRONT))
		return LOFT_FRONT;
	if (file_ok("/usr/bin/loft-front"))
		return "/usr/bin/loft-front";
	return NULL;
}

/* The notify script: the --script path from the plugin, the packaged
 * paths, then next to this binary. Falls back to the given path so the
 * error message names it. */
static const char *find_script(const char *override)
{
	static char path[512];
	char dir[512];

	if (file_ok(override))
		return override;
	if (file_ok(LOFT_SCRIPT))
		return LOFT_SCRIPT;
	if (file_ok("/usr/lib64/connman/scripts/loft-script"))
		return "/usr/lib64/connman/scripts/loft-script";
	if (self_dir(dir, sizeof(dir))) {
		snprintf(path, sizeof(path), "%s/loft-script", dir);
		if (file_ok(path))
			return path;
	}
	return override && override[0] ? override : LOFT_SCRIPT;
}

/* Host part of a URL (no scheme, port, path); IPv6 without brackets.
 * Caller frees. */
static char *host_from_url(const char *url)
{
	const char *p, *end;
	size_t n;
	char *out;

	if (!url || !url[0])
		return NULL;
	p = strstr(url, "://");
	p = p ? p + 3 : url;
	if (*p == '[') {
		end = strchr(p, ']');
		if (!end)
			return xstrdup(p);
		n = (size_t)(end - (p + 1));
		out = malloc(n + 1);
		if (!out)
			exit(2);
		memcpy(out, p + 1, n);
		out[n] = '\0';
		return out;
	}
	end = p;
	while (*end && *end != '/' && *end != ':' && *end != '?' && *end != '#')
		end++;
	n = (size_t)(end - p);
	if (n == 0)
		return NULL;
	out = malloc(n + 1);
	if (!out)
		exit(2);
	memcpy(out, p, n);
	out[n] = '\0';
	return out;
}

/* Value of key=value in a space-separated STATUS line, or NULL.
 * Caller frees. */
static char *kv(const char *line, const char *key)
{
	char pat[64];
	const char *p, *end;
	size_t n, klen;
	char *out;

	snprintf(pat, sizeof(pat), "%s=", key);
	klen = strlen(pat);
	p = line;
	while (*p) {
		if (strncmp(p, pat, klen) == 0) {
			p += klen;
			end = p;
			while (*end && *end != ' ' && *end != '\n' && *end != '\r')
				end++;
			n = (size_t)(end - p);
			out = malloc(n + 1);
			if (!out)
				exit(2);
			memcpy(out, p, n);
			out[n] = '\0';
			return out;
		}
		while (*p && *p != ' ')
			p++;
		while (*p == ' ')
			p++;
	}
	return NULL;
}

/*
 * Runs the notify script with the session details in OpenVPN's environment
 * names: ifconfig_local (overlay address), ifconfig_remote (gateway),
 * trusted_ip (the server), tun_mtu (default 1280), and one
 * "foreign_option_N=dhcp-option DNS x" per DNS server. The netmask is always
 * /24. Returns 0 when the script exited with 0.
 */
static int run_script(const char *script, const char *ip, const char *gw,
			const char *dns, const char *mtu, const char *trusted)
{
	pid_t pid;
	int st;
	char *dns_copy, *tok, *save;
	int n;

	if (!file_ok(script)) {
		fprintf(stderr, "loft-connman: no notify script %s\n", script);
		return -1;
	}
	pid = fork();
	if (pid < 0)
		return -1;
	if (pid == 0) {
		setenv("script_type", "up", 1);
		setenv("script_context", "init", 1);
		if (ip)
			setenv("ifconfig_local", ip, 1);
		if (gw && gw[0])
			setenv("ifconfig_remote", gw, 1);
		setenv("ifconfig_netmask", "255.255.255.0", 1);
		if (trusted && trusted[0])
			setenv("trusted_ip", trusted, 1);
		setenv("tun_mtu", (mtu && mtu[0]) ? mtu : "1280", 1);
		if (dns && dns[0]) {
			dns_copy = xstrdup(dns);
			n = 1;
			for (tok = strtok_r(dns_copy, ", ", &save); tok;
			     tok = strtok_r(NULL, ", ", &save)) {
				char name[32], val[80];

				snprintf(name, sizeof(name),
					"foreign_option_%d", n++);
				snprintf(val, sizeof(val),
					"dhcp-option DNS %s", tok);
				setenv(name, val, 1);
			}
			free(dns_copy);
		}
		execl(script, script, (char *)NULL);
		_exit(127);
	}
	if (waitpid(pid, &st, 0) < 0)
		return -1;
	if (WIFEXITED(st) && WEXITSTATUS(st) == 0)
		return 0;
	return -1;
}

/*
 * Parses the plugin's --flags, starts loft-front with its single-dash flags
 * (no SOCKS listener, the given tun), and reads its stdout line by line.
 * Every line is copied to stderr for the log. The first "STATUS state=on"
 * (or up) line with an ip runs the notify script once. "STATUS state=error"
 * stops reading; then the exit code of loft-front becomes ours.
 */
int main(int argc, char **argv)
{
	const char *dev = NULL, *token = NULL, *url = NULL, *type = NULL;
	const char *path = NULL, *cdn = NULL, *user = NULL, *sni = NULL;
	const char *host = NULL, *script_arg = NULL, *config = NULL;
	const char *front, *script;
	char *trusted = NULL;
	char *args[40];
	int n = 0, i, pipefd[2], notified = 0, st;
	FILE *out;
	char line[1024];
	struct sigaction sa;

	/* 1. Read the flags the ConnMan plugin built from the Loft.* properties. */
	for (i = 1; i < argc; i++) {
		if (strcmp(argv[i], "--dev") == 0 && i + 1 < argc)
			dev = argv[++i];
		else if (strcmp(argv[i], "--token") == 0 && i + 1 < argc)
			token = argv[++i];
		else if (strcmp(argv[i], "--url") == 0 && i + 1 < argc)
			url = argv[++i];
		else if (strcmp(argv[i], "--type") == 0 && i + 1 < argc)
			type = argv[++i];
		else if (strcmp(argv[i], "--path") == 0 && i + 1 < argc)
			path = argv[++i];
		else if (strcmp(argv[i], "--cdn") == 0 && i + 1 < argc)
			cdn = argv[++i];
		else if (strcmp(argv[i], "--user") == 0 && i + 1 < argc)
			user = argv[++i];
		else if (strcmp(argv[i], "--sni") == 0 && i + 1 < argc)
			sni = argv[++i];
		else if (strcmp(argv[i], "--host") == 0 && i + 1 < argc)
			host = argv[++i];
		else if (strcmp(argv[i], "--script") == 0 && i + 1 < argc)
			script_arg = argv[++i];
		else if (strcmp(argv[i], "--config") == 0 && i + 1 < argc)
			config = argv[++i];
		else {
			fprintf(stderr, "loft-connman: unknown %s\n", argv[i]);
			return 2;
		}
	}
	/* --config is accepted for the Loft.Config property but not used yet. */
	(void)config;

	if (!dev || !url) {
		fprintf(stderr, "loft-connman: --dev and --url required\n");
		return 2;
	}

	/* 2. Find the engine and the notify script. trusted is the server
	 *    host; ConnMan keeps a route to it outside the tun. */
	front = find_front();
	if (!front) {
		fprintf(stderr, "loft-connman: no loft-front\n");
		return 2;
	}
	script = find_script(script_arg);
	trusted = host ? xstrdup(host) : host_from_url(url);

	/* 3. Build the loft-front command line. */
	args[n++] = (char *)front;
	if (token) {
		args[n++] = "-token";
		args[n++] = (char *)token;
	}
	args[n++] = "-url";
	args[n++] = (char *)url;
	args[n++] = "-type";
	args[n++] = (char *)(type && type[0] ? type : "auto");
	args[n++] = "-socks";
	args[n++] = "off";
	args[n++] = "-tun";
	args[n++] = (char *)dev;
	args[n++] = "-tun-noexec";
	if (path && path[0]) {
		args[n++] = "-path";
		args[n++] = (char *)path;
	}
	if (cdn && cdn[0]) {
		args[n++] = "-cdn";
		args[n++] = (char *)cdn;
	}
	if (user && user[0]) {
		args[n++] = "-user";
		args[n++] = (char *)user;
	}
	if (sni && sni[0]) {
		args[n++] = "-sni";
		args[n++] = (char *)sni;
	}
	args[n] = NULL;

	/* 4. Pipe for loft-front's stdout, signal forwarding, then fork+exec. */
	if (pipe(pipefd) != 0) {
		perror("pipe");
		return 2;
	}

	memset(&sa, 0, sizeof(sa));
	sa.sa_handler = on_signal;
	sigemptyset(&sa.sa_mask);
	sa.sa_flags = SA_RESTART;
	sigaction(SIGTERM, &sa, NULL);
	sigaction(SIGINT, &sa, NULL);

	child_pid = fork();
	if (child_pid < 0) {
		perror("fork");
		return 2;
	}
	if (child_pid == 0) {
		int dn;

		dup2(pipefd[1], STDOUT_FILENO);
		close(pipefd[0]);
		close(pipefd[1]);
		dn = open("/dev/null", O_RDONLY);
		if (dn >= 0) {
			dup2(dn, STDIN_FILENO);
			close(dn);
		}
		execv(front, args);
		perror("exec loft-front");
		_exit(127);
	}
	close(pipefd[1]);
	out = fdopen(pipefd[0], "r");
	if (!out) {
		kill(child_pid, SIGTERM);
		return 2;
	}

	/* 5. Watch STATUS lines. A failed notify is retried on the next
	 *    "on" line because notified stays 0. */
	while (fgets(line, sizeof(line), out)) {
		char *state, *ip, *gw, *dns, *mtu;

		fputs(line, stderr);
		if (strncmp(line, "STATUS ", 7) != 0)
			continue;
		state = kv(line, "state");
		if (!state)
			continue;
		if (strcmp(state, "error") == 0) {
			free(state);
			break;
		}
		if (strcmp(state, "on") != 0 && strcmp(state, "up") != 0) {
			free(state);
			continue;
		}
		ip = kv(line, "ip");
		gw = kv(line, "gw");
		dns = kv(line, "dns");
		mtu = kv(line, "mtu");
		if (ip && ip[0] && !notified) {
			if (run_script(script, ip, gw, dns, mtu, trusted) != 0)
				fprintf(stderr, "loft-connman: notify failed\n");
			else
				notified = 1;
		}
		free(state);
		free(ip);
		free(gw);
		free(dns);
		free(mtu);
	}
	fclose(out);

	/* 6. loft-front closed its stdout (exited or is exiting): reap it and
	 *    pass on its exit status (128+signal when it was killed). */
	if (waitpid(child_pid, &st, 0) < 0) {
		free(trusted);
		return got_signal ? 0 : 1;
	}
	free(trusted);
	if (WIFEXITED(st))
		return WEXITSTATUS(st);
	if (WIFSIGNALED(st))
		return 128 + WTERMSIG(st);
	return 1;
}
