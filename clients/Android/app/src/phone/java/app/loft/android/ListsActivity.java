/*
 * Android client, phone build: the editor for split-routing lists and route
 * profiles. Split routing (split tunneling) means only some address ranges go
 * through the VPN; the rest go direct or are blocked. A single activity that
 * swaps pages (lists, exclusions, zones, services, repository catalog,
 * import, export, profiles). Edits SplitStore and ProfileStore and saves them
 * through Prefs; a running LoftVpnService notices the Prefs change, rebuilds
 * its routes through RoutePlan and re-creates the TUN.
 *
 * Words used below: a "zone" is a named group with one direction (through the
 * tunnel, around it, or blocked); a "service" is one entry in a zone (a name
 * plus prefixes and domains); an "exclusion" is an "ignore" entry whose
 * prefixes never use the tunnel; a "route profile" is a whole saved set of
 * zones and services (ProfileStore), and the "operator" profile is the one a
 * node pushes to the client, which is read-only here.
 */
package app.loft.android;

import android.app.AlertDialog;
import android.content.Intent;
import android.os.Build;
import android.os.Bundle;
import android.os.Handler;
import android.os.Looper;
import android.text.Layout;
import android.text.TextUtils;
import android.graphics.Typeface;
import android.view.Gravity;
import android.view.View;
import android.view.ViewGroup;
import android.widget.BaseAdapter;
import android.widget.Button;
import android.widget.EditText;
import android.widget.FrameLayout;
import android.widget.HorizontalScrollView;
import android.widget.LinearLayout;
import android.widget.ListView;
import android.widget.PopupMenu;
import android.widget.ScrollView;
import android.widget.TextView;
import android.widget.Toast;

import com.google.android.material.button.MaterialButton;

import java.io.ByteArrayOutputStream;
import java.io.InputStream;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.HashSet;
import java.util.List;
import java.util.Set;
import java.util.UUID;

import app.loft.engine.mobile.Mobile;

/**
 * Editor for the split-routing lists (SplitStore). Pages: zone list,
 * exclusions, zones, one zone, one service, repository, catalog,
 * import, export, route profiles and route-profile import. Back returns to
 * the list page first.
 *
 * <p>There is one activity and no fragments: show(name) throws away the
 * current page body and builds the new one in code. All work here runs on
 * the main thread except the list download in pull().
 */
public final class ListsActivity extends LoftActivity {
    /** Request code for the file picker (a .bat list on the import page, a profile on profimp). */
    private static final int REQ_IN = 8;
    /** The lists being edited: a working copy of the active route profile. */
    private SplitStore store;
    /**
     * Zone selected on the list page; also the target of the exclusions, repository and clear actions.
     */
    private String zoneId = SplitStore.GLOBAL;
    /** Name of the page currently shown (see show()). */
    private String page = "list";
    /** Id of the service or zone open on the "svc" or "zone" page. */
    private String editing;
    /** Zone whose list URL the catalog page will set, and the page to return to afterwards. */
    private String catalogZone = SplitStore.GLOBAL;
    /** Page the catalog returns to: "repo" or "zone". */
    private String catalogBack = "repo";
    /** Main-thread handler: delivers results of the background pull and runs the snackbar timer. */
    private final Handler main = new Handler(Looper.getMainLooper());
    /** Ids picked in multi-select mode on the list page; null when not selecting. */
    private Set<String> sel;
    /** State before the last delete or clear, kept while the undo snackbar is visible. */
    private SplitStore.Snap undo;
    /** The row currently swiped open, so opening another one closes it. */
    private RevealFrame openSwipe;
    /**
     * Rows read from an imported .bat file, waiting for the user to pick a zone; null when none.
     * {@link #pendingHint} is the zone named inside the file, if any.
     */
    private List<Bat.Row> pendingRows;
    /** Zone id named inside the imported file ("REM zone: id"), or "" when it names none. */
    private String pendingHint = "";
    /** Kept as one object so snack() can cancel the previous 5-second timer before starting a new one. */
    private final Runnable hideSnack = this::hideSnack;
    /** Serial of the operator profile last shown. The node bumps it when it pushes new lists. */
    private long opSerial;
    /** Bus subscription, kept in a field so onStop can unsubscribe the same object. */
    private final Bus.Listener bus = this::onBus;

    /**
     * Loads the lists and wires the header: split on/off switch, back, the "more" menu, the
     * multi-select bar (cancel, select all, delete) and the undo button of the snackbar.
     */
    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        setContentView(R.layout.activity_lists);
        // Parses the saved lists (JSON in Prefs) on the main thread.
        store = SplitStore.load(this);
        // Header switch: split routing on/off. Saved at once; the VPN service reacts to the change.
        ToggleView split = findViewById(R.id.sw_split);
        split.setChecked(Prefs.split(this));
        split.setOnCheckedChangeListener((v, on) ->
                Prefs.p(this).edit().putBoolean("split", on).apply());
        findViewById(R.id.btn_back).setOnClickListener(v -> onBackPressed());
        findViewById(R.id.btn_more).setOnClickListener(this::openMore);
        // Multi-select bar: cancel, select all (or none, when all are already picked), delete.
        findViewById(R.id.btn_sel_x).setOnClickListener(v -> {
            sel = null;
            show("list");
        });
        findViewById(R.id.btn_sel_all).setOnClickListener(v -> {
            List<String> all = new ArrayList<>();
            for (SplitStore.Svc s : store.in(zoneId, "")) {
                all.add(s.id);
            }
            if (sel != null && sel.size() == all.size()) {
                sel.clear();
            } else {
                sel = new HashSet<>(all);
            }
            show("list");
        });
        findViewById(R.id.btn_sel_del).setOnClickListener(v -> {
            if (sel == null || sel.isEmpty()) {
                return;
            }
            List<String> ids = new ArrayList<>(sel);
            sel = null;
            gone(ids);
        });
        // Undo puts back the snapshot taken before the last delete or clear.
        findViewById(R.id.snack_undo).setOnClickListener(v -> {
            if (undo != null) {
                store.restore(undo);
                undo = null;
                persist();
            }
            hideSnack();
            show(page);
        });
        show("list");
    }

    /** Listens to the Bus (in-process event bus) while visible, to notice operator pushes. */
    @Override
    protected void onStart() {
        super.onStart();
        Bus.watch(bus);
    }

    /** Stops listening when the screen is no longer visible. */
    @Override
    protected void onStop() {
        Bus.drop(bus);
        super.onStop();
    }

    /**
     * Bus event: if the node pushed a new operator profile (serial changed) while the list or
     * profiles page is open, reload the lists when that profile is active and redraw.
     * The Bus fires for every journal line and every status update of the VPN service, and
     * each call here loads and parses the whole ProfileStore to read one serial number.
     */
    private void onBus() {
        if (!"list".equals(page) && !"profiles".equals(page)) {
            return;
        }
        ProfileStore ps = ProfileStore.load(this);
        ProfileStore.Prof op = ps.get(ProfileStore.OP);
        long ser = op == null ? 0 : op.serial;
        if (ser == opSerial) {
            return;
        }
        opSerial = ser;
        if (ps.operatorActive()) {
            store = SplitStore.load(this);
        }
        show(page);
    }

    /**
     * Back leaves multi-select first, then a sub-page (catalog and profile import go back to
     * where they were opened from), and only closes the screen from the list page.
     */
    @Override
    public void onBackPressed() {
        if (sel != null) {
            sel = null;
            show("list");
            return;
        }
        if ("catalog".equals(page)) {
            show(catalogBack);
            return;
        }
        if ("profimp".equals(page)) {
            show("profiles");
            return;
        }
        if (!"list".equals(page)) {
            show("list");
            return;
        }
        super.onBackPressed();
    }

    /**
     * Saves the lists as the active document and copies them into the active route profile.
     * The VPN service reads the saved lists when it next builds routes; while it is running it
     * also listens for the "split_store" key and rebuilds routes right after this save.
     * Both writes serialize the whole document to JSON on the calling (main) thread; the disk
     * write itself is asynchronous (apply()). syncActive also loads and parses ProfileStore.
     */
    private void persist() {
        store.save(this);
        ProfileStore.syncActive(this, store);
    }

    /**
     * True when the operator's profile is active: its lists come from the node and are read-only here.
     * Not cached: each call loads and parses the whole ProfileStore from Prefs.
     */
    private boolean ro() {
        return ProfileStore.load(this).operatorActive();
    }

    /** "More" menu on the list page: start multi-select or clear the current zone. */
    private void openMore(View v) {
        if (ro()) {
            return;
        }
        PopupMenu m = new PopupMenu(this, v);
        m.getMenu().add(0, 1, 0, R.string.split_sel);
        m.getMenu().add(0, 2, 0, R.string.split_clear);
        m.setOnMenuItemClickListener(item -> {
            if (item.getItemId() == 1) {
                sel = new HashSet<>();
                show("list");
            } else {
                openClear(false);
            }
            return true;
        });
        m.show();
    }

    /**
     * Switches to page {@code p}: sets the header (normal title bar or multi-select bar), the title,
     * and rebuilds the page body from scratch. Unknown names show the list page. The previous
     * body stays on screen until the new one is ready, so a throw cannot leave a blank ink page.
     */
    private void show(String p) {
        page = p;
        // 1. Header: the multi-select bar replaces the title bar while picking on the list page;
        //    the split switch and "more" button belong to the list page only.
        boolean list = "list".equals(p);
        boolean selecting = list && sel != null;
        findViewById(R.id.head).setVisibility(selecting ? View.GONE : View.VISIBLE);
        findViewById(R.id.selbar).setVisibility(selecting ? View.VISIBLE : View.GONE);
        findViewById(R.id.sw_split).setVisibility(list && !selecting ? View.VISIBLE : View.GONE);
        findViewById(R.id.btn_more).setVisibility(list && !selecting && !ro() ? View.VISIBLE : View.GONE);
        if (selecting) {
            int n = sel.size();
            ((TextView) findViewById(R.id.sel_title)).setText(getString(R.string.split_sel_n, n));
            findViewById(R.id.btn_sel_del).setEnabled(n > 0);
        }
        // 2. Build the new body. Any RuntimeException is logged to the Journal and replaced by
        //    an error line, so a bad entry in the lists cannot crash the screen.
        TextView title = findViewById(R.id.title);
        View content;
        try {
            switch (p) {
                case "zones":
                    title.setText(R.string.zones);
                    content = zonesPage();
                    break;
                case "zone":
                    // The zone may have been deleted meanwhile; fall back to the zones page.
                    if (store.zone(editing) == null) {
                        page = "zones";
                        title.setText(R.string.zones);
                        content = zonesPage();
                    } else {
                        title.setText(R.string.zone);
                        content = zonePage();
                    }
                    break;
                case "svc":
                    title.setText(R.string.network);
                    content = svcPage();
                    break;
                case "repo":
                    title.setText(R.string.repository);
                    content = repoPage();
                    break;
                case "catalog":
                    title.setText(R.string.catalog);
                    content = catalogPage();
                    break;
                case "excl":
                    SplitStore.Zone ez = store.zone(zoneId);
                    title.setText(getString(R.string.split_excl_title, ez == null ? zoneId : ez.name));
                    content = exclPage();
                    break;
                case "imp":
                    title.setText(R.string.split_import_title);
                    content = impPage();
                    break;
                case "exp":
                    title.setText(R.string.split_export_title);
                    content = expPage();
                    break;
                case "profiles":
                    title.setText(R.string.prof_title);
                    content = profilesPage();
                    break;
                case "profimp":
                    title.setText(R.string.prof_imp);
                    content = profimpPage();
                    break;
                default:
                    title.setText(R.string.split);
                    content = listPage();
                    break;
            }
        } catch (RuntimeException e) {
            Journal.add("lists " + p + " " + e.getMessage());
            LinearLayout err = col();
            err.addView(subline(getString(R.string.lists_page_fail)));
            content = scroll(err);
        }
        // 3. Swap the body only now that the new page exists.
        ViewGroup body = findViewById(R.id.body);
        body.removeAllViews();
        body.addView(content, new FrameLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT));
    }

    /**
     * Main page: profile row, zone chips with counts, the zone's policy line, a search field and
     * the zone's services. Below them: exclusions, add, import, export and repository (only
     * export when read-only).
     */
    private View listPage() {
        LinearLayout root = col();
        root.addView(subline(Prefs.split(this)
                ? getString(R.string.split_sub) : getString(R.string.split_sub_off)));
        // Read-only when the operator's profile is active: hide every editing control.
        boolean locked = ro();
        findViewById(R.id.btn_more).setVisibility(locked ? View.GONE : View.VISIBLE);
        // Route-profile row: current profile name, "operator" tag if pushed by a node; tap opens
        // the profiles page.
        ProfileStore ps = ProfileStore.load(this);
        ProfileStore.Prof cur = ps.current();
        String meta = "";
        if (cur != null && "op".equals(cur.src)) {
            meta = getString(R.string.prof_src_operator) + " ›";
        } else {
            meta = "›";
        }
        root.addView(clickRow(getString(R.string.prof),
                (cur == null ? getString(R.string.prof_own) : profName(cur)) + "  " + meta,
                v -> show("profiles")));
        if (locked) {
            root.addView(hint(getString(R.string.prof_ro)));
            root.addView(ghostSm(getString(R.string.prof_copy), v -> copyActive()));
        }
        // Zone chips: picking one shows that zone's services.
        LinearLayout zhead = row();
        zhead.addView(label(getString(R.string.zone)),
                new LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1));
        if (!locked) {
            zhead.addView(ghostSm(getString(R.string.zones), v -> show("zones")));
        }
        root.addView(zhead);
        LinearLayout chips = new LinearLayout(this);
        chips.setOrientation(LinearLayout.HORIZONTAL);
        // store.count walks every service once per zone chip.
        for (SplitStore.Zone z : store.zones) {
            Button b = pill(z.name + " " + store.count(z.id));
            b.setSelected(z.id.equals(zoneId));
            b.setOnClickListener(v -> {
                zoneId = z.id;
                if (sel != null) {
                    sel.clear();
                }
                show("list");
            });
            chips.addView(b);
        }
        HorizontalScrollView hs = new HorizontalScrollView(this);
        hs.setHorizontalScrollBarEnabled(false);
        hs.addView(chips);
        LinearLayout.LayoutParams hsLp = new LinearLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT);
        hsLp.bottomMargin = dp(8);
        root.addView(hs, hsLp);
        root.addView(policyOf(store.zone(zoneId)));
        // Service list, filtered by the search text (search is hidden while selecting).
        EditText search = field("");
        search.setHint(getString(R.string.search));
        search.setInputType(android.text.InputType.TYPE_CLASS_TEXT);
        root.addView(search);
        // Rows shown by the ListView; refilled by the paint runnable below. A ListView (not a
        // plain LinearLayout) so only the visible rows exist as views, even for long lists.
        final List<SplitStore.Svc> rows = new ArrayList<>();
        final BaseAdapter adapter = new BaseAdapter() {
            @Override
            public int getCount() {
                return rows.size();
            }

            @Override
            public Object getItem(int i) {
                return rows.get(i);
            }

            @Override
            public long getItemId(int i) {
                return i;
            }

            @Override
            public View getView(int i, View conv, ViewGroup parent) {
                // Reuse a scrolled-off row (conv) when there is one; inflate only otherwise.
                if (conv == null) {
                    conv = getLayoutInflater().inflate(R.layout.row_split, parent, false);
                }
                bindSvcRow((RevealFrame) conv, rows.get(i), false);
                return conv;
            }
        };
        TextView empty = hint(getString(R.string.empty_zone));
        empty.setVisibility(View.GONE);
        root.addView(empty);
        ListView lv = new ListView(this);
        lv.setDivider(new android.graphics.drawable.ColorDrawable(0));
        lv.setDividerHeight(dp(6));
        lv.setSelector(new android.graphics.drawable.ColorDrawable(0));
        lv.setNestedScrollingEnabled(true);
        // Footer under the list: exclusions row and the action buttons. It is a ListView footer
        // (not a view below the list) so it scrolls with the rows. Hidden while selecting;
        // read-only mode keeps only Export.
        if (sel == null) {
            LinearLayout tail = col(false);
            int nx = store.exclCount(zoneId);
            tail.addView(clickRow(getString(R.string.split_excl), nx + " ›",
                    v -> show("excl")));
            if (!locked) {
                // "+ Add" saves an empty service right away and opens its editor.
                tail.addView(ghostSm("+ " + getString(R.string.add), v -> {
                    SplitStore.Svc s = new SplitStore.Svc();
                    s.zone = zoneId;
                    s.name = getString(R.string.service);
                    store.services.add(s);
                    persist();
                    editing = s.id;
                    show("svc");
                }));
                LinearLayout foot = row();
                LinearLayout.LayoutParams footLp = new LinearLayout.LayoutParams(
                        ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT);
                footLp.topMargin = dp(10);
                foot.setLayoutParams(footLp);
                foot.addView(mini(getString(R.string.import_routes), v -> {
                    pendingRows = null;
                    show("imp");
                }), weight(0));
                foot.addView(mini(getString(R.string.export_routes), v -> show("exp")), weight(6));
                foot.addView(mini(getString(R.string.repository), v -> show("repo")), weight(6));
                tail.addView(foot);
            } else {
                LinearLayout foot = row();
                LinearLayout.LayoutParams footLp = new LinearLayout.LayoutParams(
                        ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT);
                footLp.topMargin = dp(10);
                foot.setLayoutParams(footLp);
                foot.addView(mini(getString(R.string.export_routes), v -> show("exp")), weight(0));
                tail.addView(foot);
            }
            lv.addFooterView(tail, null, false);
        }
        lv.setAdapter(adapter);
        // Refills the rows from the store for the current zone and search text. Runs once now
        // and again after every keystroke in the search field (store.in scans all services).
        Runnable paint = () -> {
            rows.clear();
            rows.addAll(store.in(zoneId, sel != null ? "" : search.getText().toString()));
            empty.setVisibility(rows.isEmpty() ? View.VISIBLE : View.GONE);
            adapter.notifyDataSetChanged();
        };
        search.addTextChangedListener(new SimpleWatch(paint));
        search.setVisibility(sel != null ? View.GONE : View.VISIBLE);
        paint.run();
        root.addView(lv, new LinearLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT, 0, 1));
        return root;
    }

    /**
     * Exclusions page for the current zone: "ignore" entries whose prefixes never use the tunnel.
     * Allows adding one and clearing all when editable. Uses the same row layout and swipe
     * behaviour as the service list, but the swipe action reads "back" instead of "delete".
     */
    private View exclPage() {
        LinearLayout root = col();
        root.addView(subline(getString(R.string.split_excl_sub)));
        final List<SplitStore.Svc> rows = new ArrayList<>(store.excls(zoneId));
        if (rows.isEmpty()) {
            root.addView(hint(getString(R.string.split_excl_empty)));
        } else {
            final BaseAdapter adapter = new BaseAdapter() {
                @Override
                public int getCount() {
                    return rows.size();
                }

                @Override
                public Object getItem(int i) {
                    return rows.get(i);
                }

                @Override
                public long getItemId(int i) {
                    return i;
                }

                @Override
                public View getView(int i, View conv, ViewGroup parent) {
                    if (conv == null) {
                        conv = getLayoutInflater().inflate(R.layout.row_split, parent, false);
                    }
                    bindSvcRow((RevealFrame) conv, rows.get(i), true);
                    return conv;
                }
            };
            ListView lv = new ListView(this);
            lv.setAdapter(adapter);
            lv.setDivider(new android.graphics.drawable.ColorDrawable(0));
            lv.setDividerHeight(dp(6));
            lv.setSelector(new android.graphics.drawable.ColorDrawable(0));
            root.addView(lv, new LinearLayout.LayoutParams(
                    ViewGroup.LayoutParams.MATCH_PARENT, 0, 1));
        }
        if (!ro()) {
            // "+ Add" saves an empty exclusion (ignore entry) and opens it in the service editor.
            root.addView(ghostSm("+ " + getString(R.string.add), v -> {
                SplitStore.Svc s = new SplitStore.Svc();
                s.zone = zoneId;
                s.name = "ignore";
                s.src = "ignore";
                s.ignore = true;
                store.services.add(s);
                persist();
                editing = s.id;
                show("svc");
            }));
            if (!rows.isEmpty()) {
                Button clr = danger(getString(R.string.split_excl_clear), 14);
                clr.setOnClickListener(v -> openClear(true));
                root.addView(clr);
            }
        }
        return root;
    }

    /**
     * Fills one swipeable row. A service shows name, prefix preview, counts and source (list,
     * operator or mine); an exclusion shows its prefixes. Tap opens the editor or toggles the
     * pick in multi-select; long-press starts multi-select; the swipe action deletes or, for an
     * exclusion, lets the prefix use its zone again.
     */
    private void bindSvcRow(RevealFrame row, SplitStore.Svc s, boolean excl) {
        // A recycled row may still be swiped open from its previous use; snap it shut.
        row.settleNow(0);
        TextView name = row.findViewById(R.id.name);
        TextView sub = row.findViewById(R.id.sub);
        TextView meta = row.findViewById(R.id.meta);
        TextView src = row.findViewById(R.id.src);
        TextView ck = row.findViewById(R.id.ck);
        TextView act = row.findViewById(R.id.act_del);
        // An exclusion's title is all of its prefixes joined into one string.
        name.setText(excl ? SplitStore.join(s.cidrs) : s.name);
        oneLine(name);
        oneLine(sub);
        oneLine(meta);
        oneLine(src);
        if (excl) {
            name.setTypeface(android.graphics.Typeface.MONOSPACE);
            sub.setVisibility(View.GONE);
            meta.setText(s.name);
            src.setVisibility(View.GONE);
            act.setText(R.string.split_back);
        } else {
            name.setTypeface(android.graphics.Typeface.DEFAULT);
            sub.setVisibility(View.VISIBLE);
            sub.setText(SplitStore.preview(s));
            meta.setText(svcMeta(s));
            src.setVisibility(View.VISIBLE);
            src.setText("repo".equals(s.src) ? getString(R.string.from_repo)
                    : "op".equals(s.src) ? getString(R.string.prof_src_operator)
                    : getString(R.string.mine));
            act.setText(R.string.delete);
        }
        // Swipe mode: locked while picking or read-only; otherwise only a left swipe, which
        // reveals the single action on the right (delete / back).
        boolean picking = sel != null && !excl;
        boolean locked = ro();
        ck.setVisibility(picking ? View.VISIBLE : View.GONE);
        if (picking) {
            boolean on = sel.contains(s.id);
            ck.setBackgroundResource(on ? R.drawable.bg_ck_on : R.drawable.bg_ck);
            ck.setText(on ? "✓" : "");
            row.lock();
        } else if (locked) {
            row.lock();
        } else {
            row.leftOnly();
        }
        // Keep at most one row swiped open: starting a swipe closes the previous one.
        row.setListener(new RevealFrame.Listener() {
            @Override
            public void onRevealStart(RevealFrame r) {
                if (openSwipe != null && openSwipe != r) {
                    openSwipe.close();
                }
                openSwipe = r;
            }

            @Override
            public void onSettled(RevealFrame r, float tx) {
            }
        });
        // Tap: toggle the pick (redraws the whole page to update the counter), or open the editor.
        View card = row.findViewById(R.id.card);
        card.setOnClickListener(v -> {
            if (picking) {
                if (sel.contains(s.id)) {
                    sel.remove(s.id);
                } else {
                    sel.add(s.id);
                }
                show("list");
                return;
            }
            editing = s.id;
            show("svc");
        });
        // Long-press starts multi-select with this row already picked.
        card.setOnLongClickListener(v -> {
            if (excl || sel != null || locked) {
                return false;
            }
            sel = new HashSet<>();
            sel.add(s.id);
            show("list");
            return true;
        });
        act.setOnClickListener(v -> gone(java.util.Collections.singletonList(s.id)));
    }

    /** Short count line for a service, e.g. "3 networks · 2 domains". */
    private String svcMeta(SplitStore.Svc s) {
        List<String> bits = new ArrayList<>();
        if (!s.cidrs.isEmpty()) {
            bits.add(getResources().getQuantityString(R.plurals.split_nets, s.cidrs.size(), s.cidrs.size()));
        }
        if (!s.domains.isEmpty()) {
            bits.add(getResources().getQuantityString(R.plurals.split_doms, s.domains.size(), s.domains.size()));
        }
        return TextUtils.join(" · ", bits);
    }

    /**
     * Line that says what the zone does (through, around or block), with an explanation for block.
     */
    private View policyOf(SplitStore.Zone z) {
        LinearLayout box = col(false);
        String name = z == null ? zoneId : z.name;
        String line = z != null && z.block ? getString(R.string.policy_block, name)
                : z != null && z.through ? getString(R.string.policy_through, name)
                : getString(R.string.policy_around, name);
        box.addView(policyLine(line));
        if (z != null && z.block) {
            TextView d = hint(getString(R.string.split_block_d));
            LinearLayout.LayoutParams lp = (LinearLayout.LayoutParams) d.getLayoutParams();
            lp.topMargin = 0;
            lp.bottomMargin = dp(8);
            box.addView(d);
        }
        return box;
    }

    /**
     * Editor for one service or exclusion: kind (route or ignore), networks, domains, name and
     * zone. Save keeps only valid prefixes and unique lower-case domains. Read-only for the
     * operator profile. Creates an empty service if the id is unknown.
     */
    private View svcPage() {
        SplitStore.Svc s = store.svc(editing);
        // The new entry is added to the store in memory only; it is saved when the user taps Save.
        if (s == null) {
            s = new SplitStore.Svc();
            s.zone = zoneId;
            store.services.add(s);
            editing = s.id;
        }
        // Effectively-final copy, so the lambdas below can use it.
        SplitStore.Svc svc = s;
        LinearLayout root = col();
        root.addView(subline(svc.ignore
                ? getString(R.string.split_add_body_ignore) : getString(R.string.split_add_body)));
        // 1. Kind switch: route (a normal service) or ignore (an exclusion).
        LinearLayout kind = new LinearLayout(this);
        kind.setOrientation(LinearLayout.HORIZONTAL);
        Button route = chip(getString(R.string.kind_route));
        Button ign = chip(getString(R.string.kind_ignore));
        kind.addView(route);
        kind.addView(ign);
        LinearLayout.LayoutParams kindLp = new LinearLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT);
        kindLp.bottomMargin = dp(8);
        kind.setLayoutParams(kindLp);
        root.addView(kind);
        // 2. Text fields: prefixes and domains one per line, then the name.
        root.addView(label(getString(R.string.networks)));
        EditText nets = field(SplitStore.join(svc.cidrs).replace(' ', '\n'));
        nets.setMinLines(3);
        nets.setGravity(Gravity.TOP);
        root.addView(nets);
        TextView domLab = label(getString(R.string.domains));
        EditText doms = field(TextUtils.join("\n", svc.domains));
        doms.setMinLines(2);
        doms.setGravity(Gravity.TOP);
        TextView domHint = hint(getString(R.string.domains_d));
        root.addView(domLab);
        root.addView(doms);
        root.addView(domHint);
        root.addView(label(getString(R.string.name)));
        EditText name = field(svc.name);
        root.addView(name);
        // 3. Zone picker: one chip per zone. Picking one moves the entry in memory at once;
        //    it is saved with the rest on Save.
        root.addView(label(getString(R.string.zone)));
        LinearLayout zseg = new LinearLayout(this);
        zseg.setOrientation(LinearLayout.HORIZONTAL);
        int zi = 0;
        for (int i = 0; i < store.zones.size(); i++) {
            SplitStore.Zone z = store.zones.get(i);
            zseg.addView(chip(z.name));
            if (z.id.equals(svc.zone)) {
                zi = i;
            }
        }
        Seg.bind(zseg, zi, i -> svc.zone = store.zones.get(i).id);
        HorizontalScrollView hs = new HorizontalScrollView(this);
        hs.addView(zseg);
        root.addView(hs);
        // Domains apply only to route entries, so hide them for ignore.
        Runnable kindUi = () -> {
            int vis = svc.ignore ? View.GONE : View.VISIBLE;
            domLab.setVisibility(vis);
            doms.setVisibility(vis);
            domHint.setVisibility(vis);
        };
        Seg.bind(kind, svc.ignore ? 1 : 0, i -> {
            svc.ignore = i == 1;
            svc.src = svc.ignore ? "ignore" : "me";
            kindUi.run();
        });
        kindUi.run();
        // Read-only (operator profile): show the entry with every control disabled, no buttons.
        if (ro()) {
            nets.setEnabled(false);
            doms.setEnabled(false);
            name.setEnabled(false);
            for (int i = 0; i < kind.getChildCount(); i++) {
                kind.getChildAt(i).setEnabled(false);
            }
            for (int i = 0; i < zseg.getChildCount(); i++) {
                zseg.getChildAt(i).setEnabled(false);
            }
            return scroll(root);
        }
        // Save: parse the text fields back into the service.
        // Prefixes and domains may be separated by spaces, commas or new lines. Invalid prefixes
        // are dropped silently; valid ones are stored in their canonical form (Cidr.display).
        Button save = go(getString(R.string.save), 14);
        save.setOnClickListener(v -> {
            svc.name = name.getText().toString().trim();
            svc.cidrs.clear();
            for (String line : nets.getText().toString().split("[\\s,]+")) {
                Cidr c = Cidr.parse(line);
                if (c != null) {
                    svc.cidrs.add(c.display());
                }
            }
            svc.domains.clear();
            if (!svc.ignore) {
                for (String line : doms.getText().toString().split("[\\s,]+")) {
                    String d = line.trim().toLowerCase();
                    if (!d.isEmpty() && !svc.domains.contains(d)) {
                        svc.domains.add(d);
                    }
                }
            }
            persist();
            show("list");
        });
        root.addView(save);
        Button del = danger(getString(svc.ignore ? R.string.split_del_excl : R.string.split_del_svc), 8);
        // Delete goes back to the list first, so gone() redraws the list with the undo snackbar.
        del.setOnClickListener(v -> {
            String id = svc.id;
            show("list");
            gone(java.util.Collections.singletonList(id));
        });
        root.addView(del);
        return scroll(root);
    }

    /**
     * List of all zones with direction and service count, plus "new zone" (goes through by default).
     * A new zone gets a random UUID id and is saved immediately, then opened in the zone editor.
     */
    private View zonesPage() {
        LinearLayout root = col();
        for (SplitStore.Zone z : store.zones) {
            LinearLayout row = new LinearLayout(this);
            row.setOrientation(LinearLayout.VERTICAL);
            row.setBackgroundResource(R.drawable.bg_card);
            row.setPadding(dp(12), dp(10), dp(12), dp(10));
            LinearLayout.LayoutParams lp = new LinearLayout.LayoutParams(
                    ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT);
            lp.bottomMargin = dp(6);
            row.setLayoutParams(lp);
            TextView n = new TextView(this);
            n.setText(z.name);
            n.setTextColor(getColor(R.color.snow));
            row.addView(n);
            TextView m = new TextView(this);
            m.setText(dirLabel(z) + " · " + store.count(z.id));
            m.setTextColor(getColor(R.color.mute));
            m.setTextSize(10.5f);
            m.setPadding(0, dp(2), 0, 0);
            row.addView(m);
            row.setOnClickListener(v -> {
                editing = z.id;
                show("zone");
            });
            root.addView(row);
        }
        if (!ro()) {
            root.addView(ghostSm("+ " + getString(R.string.new_zone), v -> {
                SplitStore.Zone z = new SplitStore.Zone();
                z.id = UUID.randomUUID().toString();
                z.name = getString(R.string.new_zone);
                z.through = true;
                store.zones.add(z);
                persist();
                editing = z.id;
                show("zone");
            }));
        }
        return scroll(root);
    }

    /** Human label for a zone's direction: through, around or blocked. */
    private String dirLabel(SplitStore.Zone z) {
        if (z.block) {
            return getString(R.string.split_blocked);
        }
        return getString(z.through ? R.string.through_loft : R.string.around_loft);
    }

    /**
     * Editor for one zone: name (fixed for built-in zones), direction, list URL with catalog
     * picker, auto-update switch and last-download state. Custom zones can be deleted.
     */
    private View zonePage() {
        SplitStore.Zone z = store.zone(editing);
        LinearLayout root = col();
        if (z == null) {
            return root;
        }
        EditText name = field(z.name);
        name.setEnabled(!z.builtin);
        root.addView(label(getString(R.string.zone_name)));
        root.addView(name);
        // Direction switch: 0 through, 1 around, 2 block. It changes the zone in memory at once
        // (saved on Save); the block explanation shows only for block.
        root.addView(label(getString(R.string.split_rdef)));
        LinearLayout dir = new LinearLayout(this);
        dir.setOrientation(LinearLayout.HORIZONTAL);
        dir.addView(segBtn(getString(R.string.through_loft)));
        dir.addView(segBtn(getString(R.string.around_loft)));
        dir.addView(segBtn(getString(R.string.split_block)));
        TextView blockD = hint(getString(R.string.split_block_d));
        Seg.bind(dir, z.dir(), i -> {
            z.setDir(i);
            blockD.setVisibility(i == 2 ? View.VISIBLE : View.GONE);
        });
        LinearLayout.LayoutParams dirLp = new LinearLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT);
        dirLp.bottomMargin = dp(8);
        dir.setLayoutParams(dirLp);
        root.addView(dir);
        blockD.setVisibility(z.block ? View.VISIBLE : View.GONE);
        root.addView(blockD);
        root.addView(label(getString(R.string.list_url)));
        // List URL: the typed text is saved before opening the catalog, so it is not lost.
        EditText url = urlField(z.repo);
        root.addView(urlRow(url, v -> {
            z.repo = url.getText().toString().trim();
            persist();
            openCatalog(z.id, "zone");
        }));
        root.addView(autoRow(z));
        // pulled is the time of the last successful download; 0 means never.
        root.addView(hint(z.pulled == 0 ? getString(R.string.no_list) : getString(R.string.pulled)));
        if (ro()) {
            name.setEnabled(false);
            url.setEnabled(false);
            for (int i = 0; i < dir.getChildCount(); i++) {
                dir.getChildAt(i).setEnabled(false);
            }
            return scroll(root);
        }
        Button save = go(getString(R.string.save), 14);
        save.setOnClickListener(v -> {
            if (!z.builtin) {
                z.name = name.getText().toString().trim();
            }
            z.repo = url.getText().toString().trim();
            persist();
            show("zones");
        });
        root.addView(save);
        if (!z.builtin) {
            // Deleting a custom zone: the hint below tells the user where its services go.
            Button del = danger(getString(R.string.delete_zone), 8);
            del.setOnClickListener(v -> {
                store.deleteZone(z.id);
                persist();
                show("zones");
            });
            root.addView(del);
            root.addView(hint(getString(R.string.split_zones_move)));
        }
        return scroll(root);
    }

    /**
     * Repository page for the current zone: list URL (saved as typed), catalog picker,
     * auto-update and a button that downloads the list now. "Repository" here means a web
     * address of a .bat route list, usually one from the public loft-routes catalog.
     */
    private View repoPage() {
        SplitStore.Zone z = store.zone(zoneId);
        LinearLayout root = col();
        if (z == null) {
            return root;
        }
        root.addView(subline(z.name + " · " + getString(R.string.repo_d)));
        root.addView(label(getString(R.string.list_url)));
        // Saved on every keystroke: each call serializes the lists, re-parses ProfileStore and,
        // while the VPN runs, makes the service rebuild its routes.
        EditText url = urlField(z.repo);
        url.addTextChangedListener(new SimpleWatch(() -> {
            z.repo = url.getText().toString().trim();
            persist();
        }));
        root.addView(urlRow(url, v -> {
            z.repo = url.getText().toString().trim();
            persist();
            openCatalog(z.id, "repo");
        }));
        root.addView(autoRow(z));
        root.addView(hint(z.pulled == 0 ? getString(R.string.no_list) : getString(R.string.pulled)));
        Button pull = go(getString(R.string.pull), 14);
        pull.setOnClickListener(v -> {
            z.repo = url.getText().toString().trim();
            persist();
            pull(z);
        });
        root.addView(pull);
        return scroll(root);
    }

    /**
     * Import page. Without rows yet it offers a file picker; after a .bat file is read it shows
     * how many rows were found, lets the user pick the zone (the file's own zone hint first)
     * and adds the rows, skipping prefixes the zone already has.
     */
    private View impPage() {
        LinearLayout root = col();
        root.addView(subline(getString(R.string.split_import_body)));
        // Step 1: nothing read yet, offer the system file picker; the result arrives in
        // onActivityResult, which fills pendingRows and shows this page again.
        if (pendingRows == null) {
            root.addView(ghostSm(getString(R.string.from_file), v -> {
                Intent i = new Intent(Intent.ACTION_OPEN_DOCUMENT);
                i.addCategory(Intent.CATEGORY_OPENABLE);
                i.setType("*/*");
                startActivityForResult(i, REQ_IN);
            }));
            return scroll(root);
        }
        // Step 2: rows are read. Preselect the zone the file names, if it exists here, else the
        // zone open on the list page.
        root.addView(hint(getString(R.string.split_import_detect, pendingRows.size())));
        root.addView(label(getString(R.string.zone)));
        LinearLayout zseg = new LinearLayout(this);
        zseg.setOrientation(LinearLayout.HORIZONTAL);
        int zi = 0;
        String dest = pendingHint;
        if (store.zone(dest) == null) {
            dest = zoneId;
        }
        // One-element array: a mutable holder the Seg callback lambda can write to.
        final String[] hold = {dest};
        for (int i = 0; i < store.zones.size(); i++) {
            SplitStore.Zone z = store.zones.get(i);
            zseg.addView(chip(z.name));
            if (z.id.equals(dest)) {
                zi = i;
            }
        }
        Seg.bind(zseg, zi, i -> hold[0] = store.zones.get(i).id);
        HorizontalScrollView hs = new HorizontalScrollView(this);
        hs.addView(zseg);
        root.addView(hs);
        Button go = go(getString(R.string.split_import_go, pendingRows.size()), 14);
        go.setOnClickListener(v -> {
            int n = store.addRows(hold[0], pendingRows);
            pendingRows = null;
            persist();
            Toast.makeText(this, getString(R.string.split_import_go, n), Toast.LENGTH_SHORT).show();
            show("list");
        });
        root.addView(go);
        return scroll(root);
    }

    /**
     * Export page: all zones as one .bat text, shown as a preview and sent via the share sheet.
     * The text is built and laid out in full on the main thread, in a single TextView.
     */
    private View expPage() {
        LinearLayout root = col();
        root.addView(subline(getString(R.string.split_export_body)));
        String bat = Bat.emitAll(store);
        TextView prev = new TextView(this);
        prev.setText(bat);
        prev.setTextColor(getColor(R.color.stone));
        prev.setTextSize(11);
        prev.setTypeface(android.graphics.Typeface.MONOSPACE);
        prev.setPadding(dp(12), dp(10), dp(12), dp(10));
        prev.setBackgroundResource(R.drawable.bg_field);
        root.addView(prev);
        Button save = go(getString(R.string.split_export_save), 14);
        save.setOnClickListener(v -> {
            Intent i = new Intent(Intent.ACTION_SEND);
            i.setType("text/plain");
            i.putExtra(Intent.EXTRA_TEXT, bat);
            i.putExtra(Intent.EXTRA_SUBJECT, "loft-routes.bat");
            startActivity(Intent.createChooser(i, getString(R.string.export_routes)));
        });
        root.addView(save);
        return scroll(root);
    }

    /** Display name of a route profile; unnamed own profiles get the "own" label. */
    private String profName(ProfileStore.Prof p) {
        if (p == null) {
            return getString(R.string.prof_own);
        }
        if (p.name != null && !p.name.isEmpty()) {
            return p.name;
        }
        if (ProfileStore.MINE.equals(p.id) || "me".equals(p.src)) {
            return getString(R.string.prof_own);
        }
        return p.id;
    }

    /**
     * Where a route profile came from: the operator (with its connection name), a link, or the user.
     */
    private String profSrc(ProfileStore.Prof p) {
        if ("op".equals(p.src)) {
            String conn = p.conn == null || p.conn.isEmpty() ? "—" : p.conn;
            return getString(R.string.prof_src_op, conn);
        }
        if ("link".equals(p.src)) {
            return getString(R.string.prof_src_link);
        }
        return getString(R.string.prof_src_own);
    }

    /**
     * Makes an editable copy of the active (operator) profile and switches to it, then reloads
     * the lists because activation rewrote the active document (split_store).
     */
    private void copyActive() {
        ProfileStore ps = ProfileStore.load(this);
        ProfileStore.Prof src = ps.current();
        if (src == null) {
            return;
        }
        ps.copyAndActivate(src, getString(R.string.prof_copy_name, profName(src)), this);
        store = SplitStore.load(this);
        show("list");
    }

    /** Route profiles page: tap one to make it active; operator profiles also offer "copy". */
    private View profilesPage() {
        LinearLayout root = col();
        root.addView(subline(getString(R.string.prof_sub)));
        ProfileStore ps = ProfileStore.load(this);
        // One card per profile; the active one is marked with a check. Each tap handler loads a
        // fresh ProfileStore because ProfileStore objects are not shared or cached.
        for (ProfileStore.Prof pr : ps.profiles) {
            String meta = pr.id.equals(ps.active) ? "✓ " + getString(R.string.prof_active) : "";
            LinearLayout row = clickRow(profName(pr), profSrc(pr) + (meta.isEmpty() ? "" : "  " + meta), v -> {
                ProfileStore storePs = ProfileStore.load(this);
                storePs.activate(pr.id, this);
                store = SplitStore.load(this);
                show("list");
            });
            root.addView(row);
            if ("op".equals(pr.src)) {
                root.addView(ghostSm(getString(R.string.prof_copy), v -> {
                    ProfileStore s = ProfileStore.load(this);
                    ProfileStore.Prof src = s.get(pr.id);
                    if (src == null) {
                        return;
                    }
                    s.copyAndActivate(src, getString(R.string.prof_copy_name, profName(src)), this);
                    store = SplitStore.load(this);
                    show("list");
                }));
            }
        }
        root.addView(ghostSm("+ " + getString(R.string.prof_imp), v -> show("profimp")));
        return scroll(root);
    }

    /**
     * Route-profile import page: paste a loft://routes/ link or pick a file. A picked file
     * goes through onActivityResult, which sees page "profimp" and calls importProfile.
     */
    private View profimpPage() {
        LinearLayout root = col();
        root.addView(subline(getString(R.string.prof_imp_sub)));
        EditText field = field("");
        field.setHint("loft://routes/…");
        field.setTypeface(android.graphics.Typeface.MONOSPACE);
        field.setMinLines(3);
        field.setGravity(Gravity.TOP);
        root.addView(field);
        root.addView(ghostSm(getString(R.string.from_file), v -> {
            Intent i = new Intent(Intent.ACTION_OPEN_DOCUMENT);
            i.addCategory(Intent.CATEGORY_OPENABLE);
            i.setType("*/*");
            startActivityForResult(i, REQ_IN);
        }));
        Button go = go(getString(R.string.prof_imp_go), 14);
        go.setOnClickListener(v -> importProfile(field.getText().toString()));
        root.addView(go);
        return scroll(root);
    }

    /**
     * Parses a route-profile link with the Go engine (Mobile.parseRoutes) and adds it to the
     * catalog. Any failure shows one "bad link" message. The engine is the Go core of loft,
     * compiled into the app as a native library; this call runs synchronously on the main
     * thread. The new profile is added but not activated.
     */
    private void importProfile(String raw) {
        if (raw == null || raw.trim().isEmpty()) {
            Toast.makeText(this, R.string.prof_bad, Toast.LENGTH_SHORT).show();
            return;
        }
        try {
            String json = Mobile.parseRoutes(raw.trim());
            if (json == null || json.isEmpty()) {
                Toast.makeText(this, R.string.prof_bad, Toast.LENGTH_SHORT).show();
                return;
            }
            ProfileStore.Prof p = ProfileStore.importLink(this, json);
            if (p == null) {
                Toast.makeText(this, R.string.prof_bad, Toast.LENGTH_SHORT).show();
                return;
            }
            Toast.makeText(this, R.string.prof_added, Toast.LENGTH_SHORT).show();
            show("profiles");
        } catch (Exception e) {
            Toast.makeText(this, R.string.prof_bad, Toast.LENGTH_SHORT).show();
        }
    }

    /** Opens the catalog to choose a list for {@code zone}, returning to page {@code back}. */
    private void openCatalog(String zone, String back) {
        catalogZone = zone;
        catalogBack = back;
        show("catalog");
    }

    /**
     * Catalog of ready-made lists in the public loft-routes repository, named in the app's
     * language. The zone's current list is highlighted; tapping one sets the zone's list URL.
     * Rows are built without {@code FakeBoldText} or a 0-width weighted child: both have
     * left a blank body on HyperOS when the system font is extra-large.
     */
    private View catalogPage() {
        LinearLayout root = col();
        root.addView(subline(getString(R.string.catalog_d)));
        // Language code (en / ru / zh ...) used to choose each list's display name.
        String lang = Prefs.applyLang(this);
        SplitStore.Zone cur = store.zone(catalogZone);
        String current = cur != null && cur.repo != null ? cur.repo : "";
        int n = 0;
        for (String[] row : SplitStore.CATALOG) {
            if (row == null || row.length < 1 || row[0] == null || row[0].isEmpty()) {
                continue;
            }
            // One broken card is logged and skipped instead of failing the whole page.
            try {
                root.addView(catalogCard(row[0], lang, current));
                n++;
            } catch (RuntimeException e) {
                Journal.add("catalog " + row[0] + " " + e.getMessage());
            }
        }
        if (n == 0) {
            root.addView(hint(getString(R.string.catalog_empty)));
        }
        return scroll(root);
    }

    /** One loft-routes list: localized name, file, source. Tap writes the URL and goes back. */
    private View catalogCard(String id, String lang, String current) {
        String url = SplitStore.catalogUrl(id);
        LinearLayout card = row();
        card.setBackgroundResource(url.equals(current) ? R.drawable.bg_card_live : R.drawable.bg_card);
        card.setPadding(dp(12), dp(9), dp(12), dp(9));
        card.setMinimumHeight(dp(48));
        LinearLayout.LayoutParams lp = new LinearLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT);
        lp.bottomMargin = dp(6);
        card.setLayoutParams(lp);
        LinearLayout grow = new LinearLayout(this);
        grow.setOrientation(LinearLayout.VERTICAL);
        TextView name = snow(SplitStore.catalogLabel(id, lang));
        name.setTextSize(13.5f);
        // A real medium font instead of FakeBoldText (see the note on catalogPage).
        name.setTypeface(Typeface.create("sans-serif-medium", Typeface.NORMAL));
        oneLine(name);
        TextView file = new TextView(this);
        file.setText(id + ".bat");
        file.setTextColor(getColor(R.color.mute));
        file.setTextSize(10.5f);
        file.setTypeface(Typeface.MONOSPACE);
        file.setPadding(0, dp(2), 0, 0);
        oneLine(file);
        grow.addView(name);
        grow.addView(file);
        card.addView(grow, new LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1));
        TextView src = meta(getString(R.string.from_repo));
        oneLine(src);
        card.addView(src);
        card.setOnClickListener(v -> {
            SplitStore.Zone z = store.zone(catalogZone);
            if (z != null) {
                z.repo = url;
                persist();
            }
            show(catalogBack);
        });
        return card;
    }

    /**
     * Downloads the zone's list on a background thread, then saves and redraws on the main thread.
     * Network IO must stay off the main thread (Android throws NetworkOnMainThreadException and
     * the UI would freeze). The UI must only be touched on the main thread, hence main.post.
     * The thread writes into the same SplitStore object the screen edits, without locking, and
     * keeps a reference to this activity until it finishes.
     */
    private void pull(SplitStore.Zone z) {
        if (z.repo == null || z.repo.isEmpty()) {
            Toast.makeText(this, R.string.no_url, Toast.LENGTH_SHORT).show();
            return;
        }
        new Thread(() -> {
            try {
                store.pullZone(z);
                main.post(() -> {
                    persist();
                    Toast.makeText(this, R.string.pulled, Toast.LENGTH_SHORT).show();
                    show("list");
                });
            } catch (Exception e) {
                main.post(() -> Toast.makeText(this, R.string.pull_fail, Toast.LENGTH_SHORT).show());
            }
        }).start();
    }

    /**
     * Deletes services with undo: removes them, saves, keeps a snapshot for the snackbar and
     * describes what went (one name, an exclusion's prefix, or a count). Deleting entries that
     * came from a repository list turns their prefixes into exclusions; the snackbar says so.
     */
    private void gone(List<String> ids) {
        if (ro()) {
            return;
        }
        // 1. Snapshot first (for undo), then remove and save.
        SplitStore.Snap snap = store.snap();
        SplitStore.Gone g = store.removeSvcs(ids);
        if (g.n == 0) {
            return;
        }
        persist();
        undo = snap;
        // 2. Snackbar text: the one entry's name (or an exclusion's first prefix), else a count.
        //    The removed entry is looked up in the snapshot, since it is gone from the store.
        String what;
        if (g.n == 1) {
            what = getString(R.string.split_gone, "1");
            SplitStore.Svc one = null;
            for (SplitStore.Svc s : snap.services) {
                if (ids.contains(s.id)) {
                    one = s;
                    break;
                }
            }
            if (one != null) {
                what = getString(one.ignore ? R.string.split_gone_back : R.string.split_gone,
                        one.ignore && !one.cidrs.isEmpty() ? one.cidrs.get(0) : one.name);
            }
        } else {
            what = getString(R.string.split_gone_n, g.n);
        }
        if (g.toExcl) {
            what = what + " · " + getString(R.string.split_gone_excl);
        }
        snack(what);
        // 3. Stay on the list or exclusions page; from anywhere else go back to the list.
        show("list".equals(page) || "excl".equals(page) ? page : "list");
    }

    /**
     * Confirm dialog for clearing the current zone. For exclusions it clears them all. For
     * services it counts the user's own entries versus list entries and offers "mine only",
     * "all" (which also unlinks the list) or just the one choice that applies.
     */
    private void openClear(boolean excl) {
        SplitStore.Zone z = store.zone(zoneId);
        String name = z == null ? zoneId : z.name;
        // Count the user's own entries ("me") versus entries from a list or the operator.
        int mine = 0;
        int total = 0;
        for (SplitStore.Svc s : store.in(zoneId, "")) {
            total++;
            if ("me".equals(s.src)) {
                mine++;
            }
        }
        int fromList = total - mine;
        AlertDialog.Builder b = new AlertDialog.Builder(this);
        if (excl) {
            b.setTitle(getString(R.string.split_excl_clear_t, name));
            b.setMessage(R.string.split_excl_clear_sub);
            b.setPositiveButton(R.string.split_excl_clear, (d, w) -> {
                SplitStore.Snap snap = store.snap();
                store.clearZone(zoneId, "excl");
                persist();
                undo = snap;
                snack(getString(R.string.split_gone_clear, name));
                show("excl");
            });
        } else {
            b.setTitle(getString(R.string.split_clear_t, name));
            b.setMessage(fromList > 0 ? R.string.split_clear_sub : R.string.split_clear_sub1);
            if (fromList > 0 && mine > 0) {
                b.setNeutralButton(getString(R.string.split_clear_me, mine), (d, w) -> doClear("me", name));
                b.setPositiveButton(getString(R.string.split_clear_all, total), (d, w) -> doClear("all", name));
            } else if (fromList > 0) {
                b.setPositiveButton(getString(R.string.split_clear_all, total), (d, w) -> doClear("all", name));
            } else {
                b.setPositiveButton(getString(R.string.split_clear_n, mine), (d, w) -> doClear("me", name));
            }
        }
        b.setNegativeButton(R.string.cancel, null);
        b.show();
    }

    /** Clears the current zone ("me" or "all"), saves, and offers undo. */
    private void doClear(String how, String name) {
        SplitStore.Snap snap = store.snap();
        store.clearZone(zoneId, how);
        persist();
        undo = snap;
        snack(getString(R.string.split_gone_clear, name));
        show("list");
    }

    /**
     * Shows the undo snackbar for 5 seconds. A newer snackbar restarts the timer, so the undo
     * snapshot always belongs to the last action.
     */
    private void snack(String text) {
        TextView t = findViewById(R.id.snack_t);
        t.setText(text);
        findViewById(R.id.snack).setVisibility(View.VISIBLE);
        main.removeCallbacks(hideSnack);
        main.postDelayed(hideSnack, 5000);
    }

    /** Hides the snackbar and forgets the undo snapshot. */
    private void hideSnack() {
        findViewById(R.id.snack).setVisibility(View.GONE);
        main.removeCallbacks(hideSnack);
        undo = null;
    }

    /**
     * File picker result: on the profile import page imports a route profile, otherwise reads a
     * .bat list and opens the import page with its rows.
     */
    @Override
    protected void onActivityResult(int requestCode, int resultCode, Intent data) {
        super.onActivityResult(requestCode, resultCode, data);
        if (requestCode != REQ_IN || resultCode != RESULT_OK || data == null || data.getData() == null) {
            return;
        }
        // The file is read and parsed right here on the main thread; any error is shown as a toast.
        try (InputStream in = getContentResolver().openInputStream(data.getData())) {
            if (in == null) {
                return;
            }
            String body = read(in);
            if ("profimp".equals(page)) {
                importProfile(body);
                return;
            }
            pendingHint = Bat.zoneHint(body);
            pendingRows = Bat.parse(body);
            show("imp");
        } catch (Exception e) {
            Toast.makeText(this, e.getMessage(), Toast.LENGTH_SHORT).show();
        }
    }

    /** Reads a whole stream as UTF-8 text, with no size limit. */
    private static String read(InputStream in) throws Exception {
        ByteArrayOutputStream o = new ByteArrayOutputStream();
        byte[] b = new byte[4096];
        int n;
        while ((n = in.read(b)) > 0) {
            o.write(b, 0, n);
        }
        return o.toString(StandardCharsets.UTF_8.name());
    }

    /** Card row with a title on the left and grey meta text on the right. */
    private LinearLayout clickRow(String n, String m, View.OnClickListener l) {
        LinearLayout row = row();
        row.setBackgroundResource(R.drawable.bg_card);
        row.setPadding(dp(12), dp(10), dp(12), dp(10));
        LinearLayout.LayoutParams lp = new LinearLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT);
        lp.topMargin = dp(8);
        row.setLayoutParams(lp);
        TextView t = snow(n);
        t.setTextSize(13.5f);
        row.addView(t, new LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1));
        TextView meta = meta(m);
        row.addView(meta);
        row.setOnClickListener(l);
        return row;
    }

    /** Vertical container with the standard page padding. */
    private LinearLayout col() {
        return col(true);
    }

    /** Vertical container, padded or not. */
    private LinearLayout col(boolean pad) {
        LinearLayout l = new LinearLayout(this);
        l.setOrientation(LinearLayout.VERTICAL);
        if (pad) {
            int p = dp(16);
            l.setPadding(p, p / 2, p, p);
        }
        return l;
    }

    /** Horizontal container with vertically centred children. */
    private LinearLayout row() {
        LinearLayout l = new LinearLayout(this);
        l.setOrientation(LinearLayout.HORIZONTAL);
        l.setGravity(Gravity.CENTER_VERTICAL);
        return l;
    }

    /**
     * Wraps a page in a ScrollView. Child width is the viewport; height wraps. fillViewport
     * stretches a short page to the full height so its background covers the screen.
     */
    private ScrollView scroll(View v) {
        ScrollView s = new ScrollView(this);
        s.setFillViewport(true);
        s.addView(v, new FrameLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT));
        return s;
    }

    /**
     * Inflates a styled button layout (the styles live in XML). The body is passed as parent
     * only so the XML layout params are read; the button is not attached to it.
     */
    private Button inflateBtn(int layout) {
        return (Button) getLayoutInflater().inflate(layout, findViewById(R.id.body), false);
    }

    /** Full-width layout params with a top margin in dp. */
    private LinearLayout.LayoutParams stack(int top) {
        LinearLayout.LayoutParams lp = new LinearLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT);
        lp.topMargin = dp(top);
        return lp;
    }

    /** Equal-width button for a segmented control. */
    private Button segBtn(String t) {
        Button b = inflateBtn(R.layout.btn_seg);
        b.setText(t);
        b.setLayoutParams(new LinearLayout.LayoutParams(0, dp(32), 1));
        return b;
    }

    /** Small selectable chip, used for zone choices. */
    private Button chip(String t) {
        Button b = inflateBtn(R.layout.btn_chip);
        b.setText(t);
        LinearLayout.LayoutParams lp = new LinearLayout.LayoutParams(
                ViewGroup.LayoutParams.WRAP_CONTENT, ViewGroup.LayoutParams.WRAP_CONTENT);
        lp.rightMargin = dp(6);
        b.setLayoutParams(lp);
        return b;
    }

    /** Rounder chip, used for the zone chips on the list page. */
    private Button pill(String t) {
        Button b = chip(t);
        if (b instanceof MaterialButton) {
            ((MaterialButton) b).setCornerRadius(dp(18));
        }
        return b;
    }

    /** Main (accent) full-width button. */
    private Button go(String t, int top) {
        Button b = inflateBtn(R.layout.btn_go);
        b.setText(t);
        b.setLayoutParams(stack(top));
        return b;
    }

    /** Full-width button for destructive actions. */
    private Button danger(String t, int top) {
        Button b = inflateBtn(R.layout.btn_danger);
        b.setText(t);
        b.setLayoutParams(stack(top));
        return b;
    }

    /** Small outlined button with a click action. */
    private Button ghostSm(String t, View.OnClickListener l) {
        Button b = inflateBtn(R.layout.btn_ghost_sm);
        b.setText(t);
        b.setOnClickListener(l);
        LinearLayout.LayoutParams lp = new LinearLayout.LayoutParams(
                ViewGroup.LayoutParams.WRAP_CONTENT, ViewGroup.LayoutParams.WRAP_CONTENT);
        lp.topMargin = dp(6);
        b.setLayoutParams(lp);
        return b;
    }

    /** Small compact button. */
    private Button mini(String t) {
        Button b = inflateBtn(R.layout.btn_mini);
        b.setText(t);
        b.setMaxLines(1);
        b.setEllipsize(TextUtils.TruncateAt.END);
        b.setMinWidth(0);
        return b;
    }

    /**
     * One line with an ellipsis. XML {@code maxLines} is ignored on some
     * shells (MIUI) when the system font is extra-large. Setting it again in
     * code, plus horizontal scrolling and simple line breaking without
     * hyphenation, keeps the text on one line there too.
     */
    private static void oneLine(TextView t) {
        t.setMaxLines(1);
        t.setEllipsize(TextUtils.TruncateAt.END);
        t.setHorizontallyScrolling(true);
        if (Build.VERSION.SDK_INT >= 23) {
            t.setHyphenationFrequency(Layout.HYPHENATION_FREQUENCY_NONE);
            t.setBreakStrategy(Layout.BREAK_STRATEGY_SIMPLE);
        }
    }

    /** Small compact button with a click action. */
    private Button mini(String t, View.OnClickListener l) {
        Button b = mini(t);
        b.setOnClickListener(l);
        return b;
    }

    /** Full-width styled text field with the given text. */
    private EditText field(String v) {
        EditText e = new EditText(this);
        e.setText(v);
        e.setTextColor(getColor(R.color.snow));
        e.setHintTextColor(getColor(R.color.mute));
        e.setBackgroundResource(R.drawable.bg_field);
        e.setPadding(dp(12), dp(10), dp(12), dp(10));
        e.setTextSize(14);
        LinearLayout.LayoutParams lp = new LinearLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT);
        lp.topMargin = dp(4);
        lp.bottomMargin = dp(12);
        e.setLayoutParams(lp);
        return e;
    }

    /** Single-line monospace field for a list URL, sized to sit in a urlRow. */
    private EditText urlField(String v) {
        EditText e = field(v);
        e.setSingleLine(true);
        e.setEllipsize(TextUtils.TruncateAt.END);
        e.setTypeface(android.graphics.Typeface.MONOSPACE);
        e.setTextSize(12);
        e.setLayoutParams(new LinearLayout.LayoutParams(0, dp(40), 1));
        return e;
    }

    /** URL field plus a "pick list" button that opens the catalog. */
    private LinearLayout urlRow(EditText url, View.OnClickListener lists) {
        LinearLayout row = row();
        LinearLayout.LayoutParams wrap = new LinearLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT);
        wrap.topMargin = dp(4);
        wrap.bottomMargin = dp(12);
        row.setLayoutParams(wrap);
        row.addView(url);
        Button pick = mini(getString(R.string.pick_list));
        LinearLayout.LayoutParams lp = new LinearLayout.LayoutParams(
                ViewGroup.LayoutParams.WRAP_CONTENT, dp(40));
        lp.leftMargin = dp(6);
        pick.setLayoutParams(lp);
        pick.setOnClickListener(lists);
        row.addView(pick);
        return row;
    }

    /**
     * Settings row with the zone's auto-update switch, saved as soon as it changes. With
     * auto-update on, the list is downloaded again when it is older than SplitStore.AUTO_MS.
     */
    private LinearLayout autoRow(SplitStore.Zone z) {
        LinearLayout auto = row();
        auto.setPadding(0, dp(12), 0, dp(12));
        auto.setBackgroundResource(R.drawable.bg_setrow);
        LinearLayout text = col(false);
        TextView k = snow(getString(R.string.auto_update));
        k.setTextSize(14);
        TextView s = new TextView(this);
        s.setText(getString(R.string.auto_update_d));
        s.setTextColor(getColor(R.color.mute));
        s.setTextSize(11);
        s.setPadding(0, dp(2), 0, 0);
        text.addView(k);
        text.addView(s);
        auto.addView(text, new LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1));
        ToggleView sw = new ToggleView(this);
        sw.setChecked(z.auto);
        sw.setOnCheckedChangeListener((v, on) -> {
            z.auto = on;
            persist();
        });
        LinearLayout.LayoutParams tlp = new LinearLayout.LayoutParams(dp(42), dp(26));
        tlp.leftMargin = dp(12);
        auto.addView(sw, tlp);
        return auto;
    }

    /** Small grey field label. */
    private TextView label(String t) {
        TextView v = new TextView(this);
        v.setText(t);
        v.setTextColor(getColor(R.color.mute));
        v.setTextSize(12);
        return v;
    }

    /** Grey multi-line help text. */
    private TextView hint(String t) {
        TextView v = new TextView(this);
        v.setText(t);
        v.setTextColor(getColor(R.color.mute));
        v.setTextSize(12);
        v.setLineSpacing(0, 1.45f);
        LinearLayout.LayoutParams lp = new LinearLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT);
        lp.topMargin = dp(10);
        v.setLayoutParams(lp);
        return v;
    }

    /** Grey description line at the top of a page. */
    private TextView subline(String t) {
        TextView v = new TextView(this);
        v.setText(t);
        v.setTextColor(getColor(R.color.mute));
        v.setTextSize(12.5f);
        LinearLayout.LayoutParams lp = new LinearLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT);
        lp.bottomMargin = dp(12);
        v.setLayoutParams(lp);
        return v;
    }

    /** Grey line that states a zone's policy. */
    private TextView policyLine(String t) {
        TextView v = new TextView(this);
        v.setText(t);
        v.setTextColor(getColor(R.color.mute));
        v.setTextSize(12);
        LinearLayout.LayoutParams lp = new LinearLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT);
        lp.bottomMargin = dp(10);
        v.setLayoutParams(lp);
        return v;
    }

    /** Small secondary text, e.g. counts or source. */
    private TextView meta(String t) {
        TextView v = new TextView(this);
        v.setText(t);
        v.setTextColor(getColor(R.color.stone));
        v.setTextSize(11);
        return v;
    }

    /** Text in the main (light) text colour. */
    private TextView snow(String t) {
        TextView v = new TextView(this);
        v.setText(t);
        v.setTextColor(getColor(R.color.snow));
        return v;
    }

    /** Equal-share width params with a left margin in dp, for button rows. */
    private LinearLayout.LayoutParams weight(int left) {
        LinearLayout.LayoutParams lp = new LinearLayout.LayoutParams(
                0, ViewGroup.LayoutParams.WRAP_CONTENT, 1);
        lp.leftMargin = dp(left);
        return lp;
    }

    /** Converts density-independent pixels to real pixels for this screen. */
    private int dp(int v) {
        return Math.round(v * getResources().getDisplayMetrics().density);
    }

    /**
     * TextWatcher that runs one action after every text change. Android's TextWatcher has three
     * callbacks; this keeps only afterTextChanged so callers can pass a lambda. There is no
     * debounce: the action runs on every keystroke.
     */
    private static final class SimpleWatch implements android.text.TextWatcher {
        /** The action to run after each change. */
        private final Runnable r;

        /** Wraps {@code r}; it is run on the main thread after each text change. */
        SimpleWatch(Runnable r) {
            this.r = r;
        }

        @Override
        public void beforeTextChanged(CharSequence s, int a, int b, int c) {
        }

        @Override
        public void onTextChanged(CharSequence s, int a, int b, int c) {
        }

        @Override
        public void afterTextChanged(android.text.Editable s) {
            r.run();
        }
    }
}
