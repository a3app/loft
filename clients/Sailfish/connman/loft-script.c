/*
 * ConnMan VPN notify helper for loft. Same D-Bus contract as
 * openvpn-script.c: notify(ss a{ss}) of the process environment.
 *
 * loft-connman sets script_type=up, script_context=init, ifconfig_*,
 * trusted_ip, tun_mtu, foreign_option_N, then execs this binary.
 *
 * It is the last link of the chain: loft-plugin.c (inside connman-vpnd)
 * starts loft-connman.c, which starts loft-front and then this script. The
 * script only forwards its environment to connman-vpnd, where loft_notify
 * in loft-plugin.c turns it into the tun address, routes and DNS.
 */

#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <stdarg.h>
#include <syslog.h>
#include <libgen.h>

#include <dbus/dbus.h>

extern char **environ;

/* printf-style logging to syslog; this process has no terminal. */
static void print(const char *format, ...)
{
	va_list ap;

	va_start(ap, format);
	vsyslog(LOG_INFO, format, ap);
	va_end(ap);
}

/* Adds one NAME=value environment string to the D-Bus dictionary. */
static void append(DBusMessageIter *dict, char *pattern)
{
	DBusMessageIter entry;
	const char *key, *value;
	char *delim;

	delim = strchr(pattern, '=');
	if (!delim)
		return;
	*delim = '\0';
	key = pattern;
	value = delim + 1;

	dbus_message_iter_open_container(dict, DBUS_TYPE_DICT_ENTRY,
					NULL, &entry);
	dbus_message_iter_append_basic(&entry, DBUS_TYPE_STRING, &key);
	dbus_message_iter_append_basic(&entry, DBUS_TYPE_STRING, &value);
	dbus_message_iter_close_container(dict, &entry);
}

/*
 * connman-vpnd sets CONNMAN_BUSNAME / _INTERFACE / _PATH in the task's
 * environment so the script knows where to report. The whole environment
 * goes out as one notify(reason, context, dict) call, without waiting for
 * a reply; the plugin picks out the keys it knows.
 */
int main(int argc, char *argv[])
{
	DBusConnection *conn;
	DBusError error;
	DBusMessage *msg;
	DBusMessageIter iter, dict;
	char **envp, *busname, *interface, *path, *reason, *context;
	int ret = 0;

	/* 1. Learn where to report and why from the environment. */
	(void)argc;
	openlog(basename(argv[0]), LOG_NDELAY | LOG_PID, LOG_DAEMON);

	busname = getenv("CONNMAN_BUSNAME");
	interface = getenv("CONNMAN_INTERFACE");
	path = getenv("CONNMAN_PATH");
	reason = getenv("script_type");
	context = getenv("script_context");

	if (!busname || !interface || !path || !reason || !context) {
		print("Required environment variables not set; "
			"bus=%s iface=%s path=%s reason=%s context=%s",
			busname, interface, path, reason, context);
		ret = 1;
		goto out;
	}

	/* 2. Connect to the system bus and build notify(reason, context). */
	dbus_error_init(&error);
	conn = dbus_bus_get(DBUS_BUS_SYSTEM, &error);
	if (!conn) {
		if (dbus_error_is_set(&error)) {
			print("%s", error.message);
			dbus_error_free(&error);
		} else {
			print("Failed to get on system bus");
		}
		ret = 1;
		goto out;
	}

	msg = dbus_message_new_method_call(busname, path, interface, "notify");
	if (!msg) {
		dbus_connection_unref(conn);
		print("Failed to allocate method call");
		ret = 1;
		goto out;
	}

	dbus_message_set_no_reply(msg, TRUE);
	dbus_message_append_args(msg,
				DBUS_TYPE_STRING, &reason,
				DBUS_TYPE_STRING, &context,
				DBUS_TYPE_INVALID);

	/* 3. Append the whole environment as an a{ss} dictionary and send. */
	dbus_message_iter_init_append(msg, &iter);
	dbus_message_iter_open_container(&iter, DBUS_TYPE_ARRAY,
			DBUS_DICT_ENTRY_BEGIN_CHAR_AS_STRING
			DBUS_TYPE_STRING_AS_STRING DBUS_TYPE_STRING_AS_STRING
			DBUS_DICT_ENTRY_END_CHAR_AS_STRING, &dict);

	for (envp = environ; envp && *envp; envp++)
		append(&dict, *envp);

	dbus_message_iter_close_container(&iter, &dict);

	if (!dbus_connection_send(conn, msg, NULL)) {
		print("Failed to send message");
		ret = 1;
	}
	dbus_connection_flush(conn);
	dbus_message_unref(msg);
	dbus_connection_unref(conn);

out:
	closelog();
	return ret;
}
