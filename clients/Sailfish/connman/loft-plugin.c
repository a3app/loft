/*
 * ConnMan VPN plugin for loft. Type name: loft. Helper: loft-connman.
 *
 * connman-vpnd creates the tun. This driver starts loft-connman with the
 * provider properties, then apply overlay IP / DNS / MTU from the notify
 * script (same ss a{ss} contract as OpenVPN).
 *
 * Compile against the Sailfish or Aurora SDK (connman-devel). Dist does
 * not build this file on macOS. CMake sets LOFT_HELPER to the flavor
 * libexec (harbour-loft or app.loft.aurora).
 *
 * The chain on the phone is: the app asks connman-vpnd (over D-Bus) to
 * connect a VPN row of type "loft"; connman-vpnd loads this plugin, creates
 * the tun and calls loft_connect, which starts loft-connman as a ConnMan
 * task; loft-connman runs loft-front on that tun and, once the session is
 * up, runs loft-script; loft-script sends the address and DNS back to
 * connman-vpnd, which lands in loft_notify below.
 *
 * Reusing the OpenVPN notify format means ConnMan's own route and DNS
 * handling works without a loft-specific contract.
 */

#define LOFT_VPN_TYPE  "loft"
#define LOFT_VPN_LABEL "Loft"

#ifndef LOFT_HELPER
#define LOFT_HELPER "/usr/libexec/harbour-loft/loft-connman"
#endif
#ifndef SCRIPTDIR
#define SCRIPTDIR "/usr/lib/connman/scripts"
#endif

#define LOFT_VPN_HELPER LOFT_HELPER
#define LOFT_VPN_NOTIFY SCRIPTDIR "/loft-script"

#if defined(CONNMAN_PLUGIN)

#ifdef HAVE_CONFIG_H
#include <config.h>
#endif

#include <errno.h>
#include <string.h>
#include <stdio.h>
#include <net/if.h>
#include <linux/if_tun.h>

#include <glib.h>
#include <dbus/dbus.h>

#define CONNMAN_API_SUBJECT_TO_CHANGE
#include <connman/plugin.h>
#include <connman/log.h>
#include <connman/task.h>
#include <connman/ipconfig.h>
#include <connman/dbus.h>

#ifdef HAVE_CONNMAN_VPN_PLUGINS_VPN_H
#include <connman/vpn/plugins/vpn.h>
#else
#include <connman/vpn-plugin.h>
#endif

#ifndef ARRAY_SIZE
#define ARRAY_SIZE(a) (sizeof(a) / sizeof((a)[0]))
#endif

static DBusConnection *connection;

/* A VPN row property (as the app sets it over D-Bus) and the helper flag
 * it becomes. */
struct loft_opt {
	const char *cm;
	const char *arg;
};

static const struct loft_opt loft_options[] = {
	{ "Loft.Token",  "--token" },
	{ "Loft.URL",    "--url" },
	{ "Loft.Type",   "--type" },
	{ "Loft.Path",   "--path" },
	{ "Loft.CDN",    "--cdn" },
	{ "Loft.User",   "--user" },
	{ "Loft.SNI",    "--sni" },
	{ "Loft.Config", "--config" },
};

/* Per-connection state, kept as the provider's plugin data from connect
 * until the helper dies. cb is the "connect finished" callback ConnMan is
 * waiting on; it must be called exactly once. */
struct loft_data {
	struct vpn_provider *provider;
	struct connman_task *task;
	vpn_provider_connect_cb_t cb;
	void *user_data;
	char *if_name;
};

/* Reports the connect result to ConnMan once; later calls do nothing. */
static void loft_connect_done(struct loft_data *data, int err)
{
	vpn_provider_connect_cb_t cb;
	void *user_data;

	if (!data || !data->cb)
		return;
	cb = data->cb;
	user_data = data->user_data;
	data->cb = NULL;
	data->user_data = NULL;
	cb(data->provider, user_data, err);
}

static void free_loft_data(struct loft_data *data)
{
	if (!data)
		return;
	if (vpn_provider_get_plugin_data(data->provider) == data)
		vpn_provider_set_plugin_data(data->provider, NULL);
	loft_connect_done(data, EIO);
	g_free(data->if_name);
	g_free(data);
}

/* One DNS server from a foreign_option_N variable. id is N, so the
 * servers can be put back in the order the helper listed them. */
struct nameserver_entry {
	int id;
	char *nameserver;
};

/* Parses "foreign_option_N=dhcp-option DNS <addr>" (OpenVPN style).
 * Returns NULL for any other variable. */
static struct nameserver_entry *append_dns(const char *key, const char *value)
{
	struct nameserver_entry *entry;
	gchar **options;

	if (!g_str_has_prefix(key, "foreign_option_"))
		return NULL;
	options = g_strsplit(value, " ", 3);
	if (!options[0] || strcmp(options[0], "dhcp-option") != 0
	    || !options[1] || strcmp(options[1], "DNS") != 0
	    || !options[2]) {
		g_strfreev(options);
		return NULL;
	}
	entry = g_try_new0(struct nameserver_entry, 1);
	if (!entry) {
		g_strfreev(options);
		return NULL;
	}
	entry->nameserver = g_strdup(options[2]);
	entry->id = atoi(key + 15);
	g_strfreev(options);
	return entry;
}

static gint cmp_ns(gconstpointer a, gconstpointer b)
{
	const struct nameserver_entry *ea = a;
	const struct nameserver_entry *eb = b;

	if (ea->id < eb->id)
		return -1;
	if (ea->id > eb->id)
		return 1;
	return 0;
}

static void free_ns_entry(gpointer data)
{
	struct nameserver_entry *entry = data;

	g_free(entry->nameserver);
	g_free(entry);
}

/*
 * Called when loft-script reports. reason/context must be up/init: anything
 * else means the session went down, and ConnMan is told so. The dictionary
 * is the helper's environment: ifconfig_local is the overlay address,
 * ifconfig_remote the peer, trusted_ip the server (used as the gateway,
 * falling back to the row's Host), route_* extra routes, and
 * foreign_option_N the DNS servers. Without an address the connect fails.
 * The netmask defaults to /24.
 */
static int loft_notify(DBusMessage *msg, struct vpn_provider *provider)
{
	DBusMessageIter iter, dict;
	const char *reason, *context, *key, *value;
	char *address = NULL, *gateway = NULL, *peer = NULL, *netmask = NULL;
	struct connman_ipaddress *ipaddress;
	GSList *nameserver_list = NULL;
	struct loft_data *data;
	const char *signature = DBUS_TYPE_STRING_AS_STRING
				DBUS_TYPE_STRING_AS_STRING
				DBUS_TYPE_ARRAY_AS_STRING
				DBUS_DICT_ENTRY_BEGIN_CHAR_AS_STRING
				DBUS_TYPE_STRING_AS_STRING
				DBUS_TYPE_STRING_AS_STRING
				DBUS_DICT_ENTRY_END_CHAR_AS_STRING;

	if (!dbus_message_has_signature(msg, signature)) {
		connman_error("loft notify signature \"%s\" does not match "
				"expected \"%s\"",
				dbus_message_get_signature(msg), signature);
		return VPN_STATE_UNKNOWN;
	}

	dbus_message_iter_init(msg, &iter);
	dbus_message_iter_get_basic(&iter, &reason);
	dbus_message_iter_next(&iter);
	dbus_message_iter_get_basic(&iter, &context);
	dbus_message_iter_next(&iter);

	if (!provider) {
		connman_error("loft notify: no provider");
		return VPN_STATE_FAILURE;
	}

	data = vpn_provider_get_plugin_data(provider);
	DBG("%p %s %s", vpn_provider_get_name(provider), reason, context);

	if (g_strcmp0(reason, "up") != 0) {
		loft_connect_done(data, EIO);
		return VPN_STATE_DISCONNECT;
	}
	if (g_strcmp0(context, "init") != 0) {
		connman_warn("loft provider %p up notify with context %s",
				provider, context);
		loft_connect_done(data, ECONNRESET);
		if (data && data->task)
			connman_task_stop(data->task);
		return VPN_STATE_DISCONNECT;
	}

	dbus_message_iter_recurse(&iter, &dict);
	while (dbus_message_iter_get_arg_type(&dict) == DBUS_TYPE_DICT_ENTRY) {
		struct nameserver_entry *ns_entry;
		DBusMessageIter entry;

		dbus_message_iter_recurse(&dict, &entry);
		dbus_message_iter_get_basic(&entry, &key);
		dbus_message_iter_next(&entry);
		dbus_message_iter_get_basic(&entry, &value);

		if (!strcmp(key, "trusted_ip"))
			gateway = g_strdup(value);
		else if (!strcmp(key, "ifconfig_local"))
			address = g_strdup(value);
		else if (!strcmp(key, "ifconfig_netmask"))
			netmask = g_strdup(value);
		else if (!strcmp(key, "ifconfig_remote"))
			peer = g_strdup(value);
		else if (g_str_has_prefix(key, "route_"))
			vpn_provider_append_route(provider, key, value);

		ns_entry = append_dns(key, value);
		if (ns_entry)
			nameserver_list = g_slist_prepend(nameserver_list,
							ns_entry);

		dbus_message_iter_next(&dict);
	}

	if (!address) {
		g_slist_free_full(nameserver_list, free_ns_entry);
		g_free(gateway);
		g_free(peer);
		g_free(netmask);
		loft_connect_done(data, EINVAL);
		return VPN_STATE_FAILURE;
	}

	ipaddress = connman_ipaddress_alloc(AF_INET);
	if (!ipaddress) {
		g_slist_free_full(nameserver_list, free_ns_entry);
		g_free(address);
		g_free(gateway);
		g_free(peer);
		g_free(netmask);
		return VPN_STATE_FAILURE;
	}

	if (!netmask)
		netmask = g_strdup("255.255.255.0");
	if (!gateway) {
		const char *host = vpn_provider_get_string(provider, "Host");
		if (host)
			gateway = g_strdup(host);
	}

	connman_ipaddress_set_ipv4(ipaddress, address, netmask, gateway);
	connman_ipaddress_set_peer(ipaddress, peer);
	connman_ipaddress_set_p2p(ipaddress, true);
	vpn_provider_set_ipaddress(provider, ipaddress);

	if (nameserver_list) {
		char *nameservers = NULL;
		GSList *tmp;

		nameserver_list = g_slist_sort(nameserver_list, cmp_ns);
		for (tmp = nameserver_list; tmp; tmp = g_slist_next(tmp)) {
			struct nameserver_entry *ns = tmp->data;
			char *str;

			if (!nameservers) {
				nameservers = g_strdup(ns->nameserver);
				continue;
			}
			str = g_strjoin(" ", nameservers, ns->nameserver, NULL);
			g_free(nameservers);
			nameservers = str;
		}
		g_slist_free_full(nameserver_list, free_ns_entry);
		vpn_provider_set_nameservers(provider, nameservers);
		g_free(nameservers);
	}

	g_free(address);
	g_free(gateway);
	g_free(peer);
	g_free(netmask);
	connman_ipaddress_free(ipaddress);

	loft_connect_done(data, 0);
	return VPN_STATE_CONNECT;
}

/* Saves the Loft.* properties into the row's settings file so the row
 * survives a connman-vpnd restart. */
static int loft_save(struct vpn_provider *provider, GKeyFile *keyfile)
{
	const char *group = vpn_provider_get_save_group(provider);
	unsigned int i;

	for (i = 0; i < ARRAY_SIZE(loft_options); i++) {
		const char *option = vpn_provider_get_string(provider,
							loft_options[i].cm);
		if (!option)
			continue;
		g_key_file_set_string(keyfile, group, loft_options[i].cm,
					option);
	}
	return 0;
}

/* Adds "arg value" to the helper's command line when the property is set. */
static int add_opt(struct connman_task *task, struct vpn_provider *provider,
			const char *cm, const char *arg)
{
	const char *v = vpn_provider_get_string(provider, cm);

	if (!v || v[0] == '\0')
		return 0;
	return connman_task_add_argument(task, arg, v);
}

static void loft_died(struct connman_task *task, int exit_code, void *user_data)
{
	struct loft_data *data = user_data;

	vpn_died(task, exit_code, data->provider);
	free_loft_data(data);
}

/*
 * Starts loft-connman for this row: the tun name ConnMan created, the
 * notify script, the host, and every Loft.* property as a flag. A row with
 * no Loft.URL uses Host as the URL. Returns -EINPROGRESS: the real result
 * comes later through loft_notify or loft_died.
 */
static int loft_connect(struct vpn_provider *provider,
			struct connman_task *task, const char *if_name,
			vpn_provider_connect_cb_t cb, const char *dbus_sender,
			void *user_data)
{
	struct loft_data *data;
	const char *host;
	const char *url;
	unsigned int i;
	int err;

	(void)dbus_sender;

	data = g_try_new0(struct loft_data, 1);
	if (!data) {
		cb(provider, user_data, ENOMEM);
		return -ENOMEM;
	}
	data->provider = provider;
	data->task = task;
	data->cb = cb;
	data->user_data = user_data;
	data->if_name = g_strdup(if_name);
	vpn_provider_set_plugin_data(provider, data);

	connman_task_add_argument(task, "--dev", if_name);
	connman_task_add_argument(task, "--script", LOFT_VPN_NOTIFY);

	url = vpn_provider_get_string(provider, "Loft.URL");
	host = vpn_provider_get_string(provider, "Host");
	if (host)
		connman_task_add_argument(task, "--host", host);
	if (!url && host)
		connman_task_add_argument(task, "--url", host);

	for (i = 0; i < ARRAY_SIZE(loft_options); i++) {
		if (add_opt(task, provider, loft_options[i].cm,
				loft_options[i].arg)) {
			data->cb = NULL;
			data->user_data = NULL;
			free_loft_data(data);
			return -EIO;
		}
	}

	err = connman_task_run(task, loft_died, data, NULL, NULL, NULL);
	if (err < 0) {
		data->cb = NULL;
		data->user_data = NULL;
		connman_error("loft-connman failed to start");
		free_loft_data(data);
		return -EIO;
	}
	return -EINPROGRESS;
}

static int loft_disconnect(struct vpn_provider *provider)
{
	struct loft_data *data = vpn_provider_get_plugin_data(provider);

	if (data && data->task)
		connman_task_stop(data->task);
	return 0;
}

static int loft_device_flags(struct vpn_provider *provider)
{
	(void)provider;
	return IFF_TUN;
}

/* Tells ConnMan how to read route_network_N / route_netmask_N /
 * route_gateway_N (IPv4, OpenVPN naming) from the notify dictionary. */
static int loft_route_env_parse(struct vpn_provider *provider, const char *key,
			int *family, unsigned long *idx,
			enum vpn_provider_route_type *type)
{
	char *end;
	const char *start;

	(void)provider;
	if (g_str_has_prefix(key, "route_network_")) {
		start = key + strlen("route_network_");
		*type = VPN_PROVIDER_ROUTE_TYPE_ADDR;
	} else if (g_str_has_prefix(key, "route_netmask_")) {
		start = key + strlen("route_netmask_");
		*type = VPN_PROVIDER_ROUTE_TYPE_MASK;
	} else if (g_str_has_prefix(key, "route_gateway_")) {
		start = key + strlen("route_gateway_");
		*type = VPN_PROVIDER_ROUTE_TYPE_GW;
	} else {
		return -EINVAL;
	}
	*family = AF_INET;
	*idx = g_ascii_strtoull(start, &end, 10);
	return 0;
}

static struct vpn_driver vpn_driver = {
	.notify = loft_notify,
	.connect = loft_connect,
	.disconnect = loft_disconnect,
	.save = loft_save,
	.device_flags = loft_device_flags,
	.route_env_parse = loft_route_env_parse,
};

static int loft_init(void)
{
	connection = connman_dbus_get_connection();
	return vpn_register(LOFT_VPN_TYPE, &vpn_driver, LOFT_HELPER);
}

static void loft_exit(void)
{
	vpn_unregister(LOFT_VPN_TYPE);
	if (connection)
		dbus_connection_unref(connection);
}

CONNMAN_PLUGIN_DEFINE(loft, LOFT_VPN_LABEL, CONNMAN_VERSION,
		CONNMAN_PLUGIN_PRIORITY_DEFAULT, loft_init, loft_exit)

#endif /* CONNMAN_PLUGIN */

/* Exported outside the plugin build too, so the names can be checked
 * without the ConnMan SDK. */
const char loft_vpn_type[] = LOFT_VPN_TYPE;
const char loft_vpn_label[] = LOFT_VPN_LABEL;
const char loft_vpn_helper[] = LOFT_VPN_HELPER;
const char loft_vpn_notify[] = LOFT_VPN_NOTIFY;
