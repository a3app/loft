/*
 * Android client, phone build: the editor for split-routing lists and route
 * profiles. Split routing (split tunneling) means only some address ranges go
 * through the VPN; the rest go direct or are blocked. A single activity that
 * swaps pages (lists, exclusions, zones, services, repository catalog,
 * import, export, profiles). Edits SplitStore and ProfileStore and saves them
 * as documents (Doc); a running LoftVpnService notices the "split_store"
 * change, rebuilds its routes through RoutePlan and re-creates the TUN.
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
import android.content.Context;
import android.content.Intent;
import android.net.Uri;
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
import java.io.IOException;
import java.io.InputStream;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.HashMap;
import java.util.HashSet;
import java.util.List;
import java.util.Map;
import java.util.Set;
import java.util.UUID;
import java.util.function.Consumer;

import app.loft.engine.mobile.Mobile;

/**
 * Editor for the split-routing lists (SplitStore). Pages: zone list,
 * exclusions, zones, one zone, one service, repository, catalog,
 * import, export, route profiles and route-profile import. Back returns to
 * the list page first.
 *
 * <p>There is one activity and no fragments: show(name) throws away the
 * current page body and builds the new one in code.
 *
 * <p><b>Keeping the main thread free.</b> The lists can hold tens of
 * thousands of prefixes, and the route profile catalog holds a full copy of
 * every profile's lists, so parsing or writing either one is slow. This
 * screen therefore: keeps one parsed copy of the catalog and re-reads it only
 * when the document really changes (see {@link #prof()}); saves typed text a
 * moment after the last key instead of on every key (see {@link #persistSoon});
 * and runs downloads, file reads, profile switching, profile import and the
 * export text in the background with Ui.bg, applying the results on the main
 * thread.
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
    /**
     * Main-thread handler for this screen's own timers: the snackbar, the delayed save and the
     * delayed search. Everything posted here is removed in onDestroy.
     */
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

    /** How long the search field waits after the last key before filtering the rows. */
    private static final long SEARCH_MS = 150;
    /** How long a typed field (the list URL) waits after the last key before it is saved. */
    private static final long SAVE_MS = 400;
    /**
     * Longest export preview shown on the page, in characters. Laying out megabytes of text in
     * one TextView freezes the screen; the share button still sends the whole text.
     */
    private static final int PREVIEW_MAX = 32 * 1024;

    /**
     * Parsed copy of the route profile catalog (Doc.PROFILES), or null before the first use.
     * Parsing it is slow (it holds every profile's lists), so it is parsed once and reused until
     * the document changes; see {@link #prof()}.
     */
    private ProfileStore profCache;
    /**
     * True when the catalog document changed since {@link #profCache} was parsed. Set by the
     * document listener, which may run on any thread, hence volatile.
     */
    private volatile boolean profStale = true;
    /**
     * True when "split_store" was changed by someone else (the VPN service applying an operator
     * push, or its daily list update) since the screen last looked. Set from any thread.
     */
    private volatile boolean splitStale;
    /**
     * True while this screen itself saves on the main thread, so the document listener can tell
     * our own saves (nothing to re-read) from changes made elsewhere.
     */
    private boolean writing;
    /** True while a keystroke-driven save is waiting for {@link #SAVE_MS}; see persistSoon. */
    private boolean persistPending;
    /** The delayed save started by persistSoon; one object so it can be cancelled. */
    private final Runnable persistLater = this::persist;
    /**
     * Counts changes to {@link #store}. A background job that started from one state of the lists
     * compares it afterwards to learn whether the user changed anything meanwhile.
     */
    private long gen;
    /**
     * True while a background profile switch or import runs. Further profile taps are ignored
     * meanwhile, so two jobs never rewrite the catalog at the same time.
     */
    private boolean busy;
    /** Whether onStart already ran once (the first start needs no refresh: onCreate just loaded). */
    private boolean started;
    /** "Read-only" (operator profile active) as worked out once for the page being built. */
    private boolean roNow;
    /** Adapter of the list page's service rows, so a pick can refresh rows without a rebuild. */
    private BaseAdapter listAdapter;
    /** Refills the list page's rows from the store and the search text; null on other pages. */
    private Runnable listPaint;
    /** The delayed search filter (see SEARCH_MS); one object so it can be cancelled. */
    private final Runnable searchLater = () -> {
        if (listPaint != null) {
            listPaint.run();
        }
    };
    /** Main-thread check after a document changed elsewhere; one object so repeated posts merge. */
    private final Runnable docCheck = this::docCheck;
    /**
     * Document listener: remembers which document changed and asks the main thread to look.
     * Runs on the thread that saved (often the VPN service's own thread), so it only sets flags
     * and posts; it never parses anything itself. Our own main-thread saves are skipped.
     */
    private final Doc.Listener docs = name -> {
        if (Looper.myLooper() == Looper.getMainLooper() && writing) {
            return;
        }
        if (Doc.PROFILES.equals(name)) {
            profStale = true;
        } else if (Doc.SPLIT.equals(name)) {
            splitStale = true;
        } else {
            return;
        }
        // removeCallbacks + post: a burst of changes runs the check only once.
        main.removeCallbacks(docCheck);
        main.post(docCheck);
    };

    /**
     * Loads the lists and wires the header: split on/off switch, back, the "more" menu, the
     * multi-select bar (cancel, select all, delete) and the undo button of the snackbar.
     */
    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        setContentView(R.layout.activity_lists);
        // Parses the saved lists (the "split_store" document) on the main thread.
        store = SplitStore.load(this);
        // Remember the operator serial we start with, so the first document check does not
        // rebuild the page for a push that is already shown.
        ProfileStore.Prof op0 = prof().get(ProfileStore.OP);
        opSerial = op0 == null ? 0 : op0.serial;
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
            // Only the ticks and the counter change: refresh them, do not rebuild the page.
            refreshPicks();
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
            // A background profile switch or import is rewriting the documents right now;
            // restoring the old lists in the middle of it could mix two profiles.
            if (busy) {
                return;
            }
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

    /**
     * Listens for changes of the two documents while visible, to notice operator pushes. This
     * used to listen to the Bus, which fires for every log line and status update; the document
     * listener fires only when a document really changed.
     */
    @Override
    protected void onStart() {
        super.onStart();
        Doc.watch(docs);
        // Changes made while the screen was hidden were not heard: look once now. Not on the
        // first start, because onCreate has just read everything.
        if (started) {
            profStale = true;
            splitStale = true;
            main.removeCallbacks(docCheck);
            main.post(docCheck);
        }
        started = true;
    }

    /** Stops listening when the screen is no longer visible. */
    @Override
    protected void onStop() {
        Doc.drop(docs);
        super.onStop();
    }

    /**
     * Leaving the screen (or covering it) saves a typed change that is still waiting for its
     * delay, so nothing typed is lost.
     */
    @Override
    protected void onPause() {
        if (persistPending) {
            persist();
        }
        super.onPause();
    }

    /** Drops every timer of this screen (snackbar, delayed save, delayed search, document check). */
    @Override
    protected void onDestroy() {
        if (persistPending) {
            persist();
        }
        main.removeCallbacksAndMessages(null);
        super.onDestroy();
    }

    /**
     * Runs on the main thread after a document changed elsewhere. Only the list and profiles
     * pages show operator data, so other pages are left alone (show() looks again when one of
     * those two pages opens). Skipped while a profile job of ours is running: its own result
     * redraws the page.
     */
    private void docCheck() {
        if (busy || (!"list".equals(page) && !"profiles".equals(page))) {
            return;
        }
        if (syncDocs()) {
            show(page);
        }
    }

    /**
     * Applies changes made elsewhere: a new operator serial (the node pushed new lists) and, while
     * the operator profile is active, a new "split_store" (the service wrote the operator's lists
     * there). In both cases the lists are reloaded when they are read-only here, since there are
     * no local edits to keep then. Returns true when the page should be redrawn. Parses the
     * catalog only when it changed (see prof()).
     */
    private boolean syncDocs() {
        ProfileStore ps = prof();
        ProfileStore.Prof op = ps.get(ProfileStore.OP);
        long ser = op == null ? 0 : op.serial;
        boolean redraw = false;
        boolean reload = false;
        if (ser != opSerial) {
            opSerial = ser;
            redraw = true;
            reload = ps.operatorActive();
        }
        if (splitStale) {
            splitStale = false;
            // With an editable profile the screen's own copy wins, as before: it is saved over
            // "split_store" on the next edit.
            if (ps.operatorActive()) {
                reload = true;
                redraw = true;
            }
        }
        if (reload) {
            store = SplitStore.load(this);
            gen++;
        }
        return redraw;
    }

    /**
     * The route profile catalog, parsed once and reused. It is parsed again only after the
     * document changed somewhere else (profStale), because parsing means reading every profile's
     * lists. Our own changes go through this same object, so it stays up to date with them.
     */
    private ProfileStore prof() {
        if (profCache == null || profStale) {
            // Cleared before parsing: a change that lands during the parse sets it again.
            profStale = false;
            // load() may create and save the catalog on first use; that is our own save. The
            // old value is put back because persist() calls this while already saving.
            boolean was = writing;
            writing = true;
            try {
                profCache = ProfileStore.load(this);
            } finally {
                writing = was;
            }
        }
        return profCache;
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
     * also listens for "split_store" and rebuilds routes after this save. Both documents are
     * turned into JSON here on the main thread; the files are written in the background, and a
     * document that did not change is not written at all (see Doc).
     *
     * <p>This does the same as ProfileStore.syncActive, but on the cached catalog: syncActive
     * loads and parses the whole catalog first, on every save.
     */
    private void persist() {
        main.removeCallbacks(persistLater);
        persistPending = false;
        gen++;
        writing = true;
        try {
            store.save(this);
            ProfileStore ps = prof();
            ProfileStore.Prof p = ps.current();
            // The operator's profile belongs to the node: never overwrite it with local lists.
            if (p != null && !"op".equals(p.src)) {
                // A copy, so later edits of the screen's store do not change the catalog entry.
                p.store = store.copy();
                p.updated = System.currentTimeMillis();
                ps.save(this);
            }
        } finally {
            writing = false;
        }
    }

    /**
     * Saves a little later: for changes made key by key in a text field. Each key restarts the
     * {@link #SAVE_MS} wait, so a typed URL is saved once, after the user stops typing, instead of
     * on every key. Leaving the page or the screen saves at once (show(), onPause).
     */
    private void persistSoon() {
        gen++;
        persistPending = true;
        main.removeCallbacks(persistLater);
        main.postDelayed(persistLater, SAVE_MS);
    }

    /**
     * True when the operator's profile is active: its lists come from the node and are read-only
     * here. Cheap: it reads the cached catalog (see prof()).
     */
    private boolean ro() {
        return prof().operatorActive();
    }

    /**
     * Updates the multi-select bar (counter and delete button) and redraws the visible rows'
     * ticks, without rebuilding the page. Falls back to a full rebuild when the list page is not
     * the one showing a selection.
     */
    private void refreshPicks() {
        if (!"list".equals(page) || sel == null || listAdapter == null) {
            show("list");
            return;
        }
        selHeader();
        listAdapter.notifyDataSetChanged();
    }

    /** Multi-select bar text: how many rows are picked; delete is enabled only with a pick. */
    private void selHeader() {
        int n = sel.size();
        Ui.text(findViewById(R.id.sel_title), getString(R.string.split_sel_n, n));
        findViewById(R.id.btn_sel_del).setEnabled(n > 0);
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
        // Leaving the page: save a typed change that is still waiting, and forget the old
        // page's delayed search and row adapter.
        if (persistPending) {
            persist();
        }
        main.removeCallbacks(searchLater);
        listPaint = null;
        listAdapter = null;
        // The list and profiles pages show operator data: pick up a push that arrived while
        // another page was open. Cheap when nothing changed.
        if (!busy && ("list".equals(p) || "profiles".equals(p))) {
            syncDocs();
        }
        // Worked out once per page build; row binding reads this instead of asking per row.
        roNow = ro();
        page = p;
        // 1. Header: the multi-select bar replaces the title bar while picking on the list page;
        //    the split switch and "more" button belong to the list page only.
        boolean list = "list".equals(p);
        boolean selecting = list && sel != null;
        findViewById(R.id.head).setVisibility(selecting ? View.GONE : View.VISIBLE);
        findViewById(R.id.selbar).setVisibility(selecting ? View.VISIBLE : View.GONE);
        findViewById(R.id.sw_split).setVisibility(list && !selecting ? View.VISIBLE : View.GONE);
        findViewById(R.id.btn_more).setVisibility(list && !selecting && !roNow ? View.VISIBLE : View.GONE);
        if (selecting) {
            selHeader();
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
        boolean locked = roNow;
        findViewById(R.id.btn_more).setVisibility(locked ? View.GONE : View.VISIBLE);
        // Route-profile row: current profile name, "operator" tag if pushed by a node; tap opens
        // the profiles page. Read from the cached catalog (see prof()).
        ProfileStore.Prof cur = prof().current();
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
        // Service count of every zone, worked out once for the page.
        Map<String, Integer> counts = zoneCounts();
        for (SplitStore.Zone z : store.zones) {
            Button b = pill(z.name + " " + countOf(counts, z.id));
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
                    conv = newRow(parent);
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
        // and again after typing in the search field. store.in scans every service, so typing
        // waits SEARCH_MS after the last key instead of filtering on every key.
        Runnable paint = () -> {
            rows.clear();
            rows.addAll(store.in(zoneId, sel != null ? "" : search.getText().toString()));
            Ui.shown(empty, rows.isEmpty());
            adapter.notifyDataSetChanged();
        };
        listAdapter = adapter;
        listPaint = paint;
        search.addTextChangedListener(new SimpleWatch(() -> {
            main.removeCallbacks(searchLater);
            main.postDelayed(searchLater, SEARCH_MS);
        }));
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
                        conv = newRow(parent);
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
        if (!roNow) {
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
    /**
     * Inflates one list row and sets its one-line text rules. Done once per new row rather than
     * on every bind: each of these calls asks for a new layout pass, and binding happens for
     * every row that scrolls into view.
     */
    private View newRow(ViewGroup parent) {
        View v = getLayoutInflater().inflate(R.layout.row_split, parent, false);
        oneLine(v.findViewById(R.id.name));
        oneLine(v.findViewById(R.id.sub));
        oneLine(v.findViewById(R.id.meta));
        oneLine(v.findViewById(R.id.src));
        return v;
    }

    private void bindSvcRow(RevealFrame row, SplitStore.Svc s, boolean excl) {
        // A recycled row may still be swiped open from its previous use; snap it shut.
        row.settleNow(0);
        TextView name = row.findViewById(R.id.name);
        TextView sub = row.findViewById(R.id.sub);
        TextView meta = row.findViewById(R.id.meta);
        TextView src = row.findViewById(R.id.src);
        TextView ck = row.findViewById(R.id.ck);
        TextView act = row.findViewById(R.id.act_del);
        // An exclusion's title is its prefixes: the first one plus how many more, as other list
        // rows do. An entry can hold thousands, and joining them all made one huge string per
        // row on every bind. The editor still shows every prefix.
        // Texts are set through Ui.text, which skips a value the view already shows.
        Ui.text(name, excl ? SplitStore.preview(s.cidrs) : s.name);
        if (excl) {
            name.setTypeface(android.graphics.Typeface.MONOSPACE);
            Ui.shown(sub, false);
            Ui.text(meta, s.name);
            Ui.shown(src, false);
            act.setText(R.string.split_back);
        } else {
            name.setTypeface(android.graphics.Typeface.DEFAULT);
            Ui.shown(sub, true);
            Ui.text(sub, SplitStore.preview(s));
            Ui.text(meta, svcMeta(s));
            Ui.shown(src, true);
            Ui.text(src, "repo".equals(s.src) ? getString(R.string.from_repo)
                    : "op".equals(s.src) ? getString(R.string.prof_src_operator)
                    : getString(R.string.mine));
            act.setText(R.string.delete);
        }
        // Swipe mode: locked while picking or read-only; otherwise only a left swipe, which
        // reveals the single action on the right (delete / back). Read-only was worked out once
        // for the page (roNow), not per row.
        boolean picking = sel != null && !excl;
        boolean locked = roNow;
        Ui.shown(ck, picking);
        if (picking) {
            boolean on = sel.contains(s.id);
            Ui.bgRes(ck, on ? R.drawable.bg_ck_on : R.drawable.bg_ck);
            Ui.text(ck, on ? "✓" : "");
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
        // Tap: toggle the pick (refreshes the counter and the ticks, keeping the page and its
        // scroll position), or open the editor.
        View card = row.findViewById(R.id.card);
        card.setOnClickListener(v -> {
            if (picking) {
                if (sel.contains(s.id)) {
                    sel.remove(s.id);
                } else {
                    sel.add(s.id);
                }
                refreshPicks();
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

    /**
     * Number of non-ignore services in every zone, by zone id, in one walk over the services.
     * Gives the same numbers as store.count, which walks all services once per zone.
     */
    private Map<String, Integer> zoneCounts() {
        Map<String, Integer> counts = new HashMap<>();
        for (SplitStore.Svc s : store.services) {
            if (!s.ignore) {
                Integer c = counts.get(s.zone);
                counts.put(s.zone, c == null ? 1 : c + 1);
            }
        }
        return counts;
    }

    /** A zone's count from {@link #zoneCounts()}; 0 for a zone with no services. */
    private static int countOf(Map<String, Integer> counts, String zoneId) {
        Integer c = counts.get(zoneId);
        return c == null ? 0 : c;
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
        Map<String, Integer> counts = zoneCounts();
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
            m.setText(dirLabel(z) + " · " + countOf(counts, z.id));
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
        // Saved as typed, but only SAVE_MS after the last key (persistSoon): a save turns both
        // documents into JSON, and saving on every key made typing stutter. The value in memory
        // is updated at once, so the buttons below always see what is typed.
        EditText url = urlField(z.repo);
        url.addTextChangedListener(new SimpleWatch(() -> {
            z.repo = url.getText().toString().trim();
            persistSoon();
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
     * The text is built in the background from a copy of the lists (Bat.emitAll walks every
     * service once per zone); the preview and the share button fill in when it is ready. A very
     * long text is shown cut to PREVIEW_MAX characters, because laying out megabytes in one
     * TextView freezes the screen; sharing always sends the whole text.
     */
    private View expPage() {
        LinearLayout root = col();
        root.addView(subline(getString(R.string.split_export_body)));
        // The full text once built; a one-element array so the lambdas below can fill and read it.
        final String[] bat = {null};
        TextView prev = new TextView(this);
        prev.setTextColor(getColor(R.color.stone));
        prev.setTextSize(11);
        prev.setTypeface(android.graphics.Typeface.MONOSPACE);
        prev.setPadding(dp(12), dp(10), dp(12), dp(10));
        prev.setBackgroundResource(R.drawable.bg_field);
        root.addView(prev);
        Button save = go(getString(R.string.split_export_save), 14);
        // Nothing to share until the text is ready.
        save.setEnabled(false);
        save.setOnClickListener(v -> {
            if (bat[0] == null) {
                return;
            }
            Intent i = new Intent(Intent.ACTION_SEND);
            i.setType("text/plain");
            i.putExtra(Intent.EXTRA_TEXT, bat[0]);
            i.putExtra(Intent.EXTRA_SUBJECT, "loft-routes.bat");
            startActivity(Intent.createChooser(i, getString(R.string.export_routes)));
        });
        root.addView(save);
        // The copy is taken here on the main thread, so the background job never reads the
        // store while the screen changes it.
        SplitStore snap = store.copy();
        Ui.bg(this, () -> Bat.emitAll(snap), (text, err) -> {
            // The user may have left the export page (or opened it again) meanwhile; this
            // result belongs to the views built above only while they are still on screen.
            if (err != null || text == null || !prev.isAttachedToWindow()) {
                return;
            }
            bat[0] = text;
            prev.setText(previewOf(text));
            save.setEnabled(true);
        });
        return scroll(root);
    }

    /**
     * The export text as shown on the page: whole when short, otherwise cut at the last full
     * line before PREVIEW_MAX characters, with "…" on its own line to show that more follows.
     */
    private static String previewOf(String text) {
        if (text.length() <= PREVIEW_MAX) {
            return text;
        }
        int cut = text.lastIndexOf('\n', PREVIEW_MAX);
        if (cut <= 0) {
            cut = PREVIEW_MAX;
        }
        return text.substring(0, cut) + "\n…";
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
        profileJob(ps -> {
            ProfileStore.Prof src = ps.current();
            if (src != null) {
                ps.copyAndActivate(src, getString(R.string.prof_copy_name, profName(src)), getApplicationContext());
            }
        });
    }

    /**
     * Runs a change of the profile catalog (switching or copying a profile) in the background,
     * then reloads the lists and shows the list page. Switching reads and writes both documents
     * (it saves the outgoing lists into their profile, then writes the new profile's lists as
     * "split_store") and parses the catalog and the lists, which froze the screen on the main
     * thread. While the job runs, further taps are ignored (busy).
     *
     * <p>The job gets a fresh catalog, loaded on its own thread; the screen's cached catalog is
     * marked stale afterwards and parsed again when next needed.
     */
    private void profileJob(Consumer<ProfileStore> change) {
        if (busy) {
            return;
        }
        // The outgoing profile is saved from "split_store", so a typed change still waiting for
        // its delay must reach the document first.
        if (persistPending) {
            persist();
        }
        busy = true;
        Context app = getApplicationContext();
        Ui.bg(this, () -> {
            ProfileStore ps = ProfileStore.load(app);
            change.accept(ps);
            // The lists of the profile that is active now, parsed here rather than on the main thread.
            return SplitStore.load(app);
        }, (st, err) -> {
            busy = false;
            // Both documents were rewritten by the job: re-read the catalog when next needed,
            // and the lists are replaced right here.
            profStale = true;
            splitStale = false;
            if (err != null || st == null) {
                Journal.add("lists profile " + (err == null ? "no lists" : err.getMessage()));
                return;
            }
            store = st;
            gen++;
            show("list");
        });
    }

    /** Route profiles page: tap one to make it active; operator profiles also offer "copy". */
    private View profilesPage() {
        LinearLayout root = col();
        root.addView(subline(getString(R.string.prof_sub)));
        // The cached catalog (see prof()), only read here.
        ProfileStore ps = prof();
        // One card per profile; the active one is marked with a check. A tap switches profiles
        // in the background (profileJob), which works on a freshly loaded catalog.
        for (ProfileStore.Prof pr : ps.profiles) {
            String meta = pr.id.equals(ps.active) ? "✓ " + getString(R.string.prof_active) : "";
            LinearLayout row = clickRow(profName(pr), profSrc(pr) + (meta.isEmpty() ? "" : "  " + meta),
                    v -> profileJob(job -> job.activate(pr.id, getApplicationContext())));
            root.addView(row);
            if ("op".equals(pr.src)) {
                root.addView(ghostSm(getString(R.string.prof_copy), v -> profileJob(job -> {
                    ProfileStore.Prof src = job.get(pr.id);
                    if (src != null) {
                        job.copyAndActivate(src, getString(R.string.prof_copy_name, profName(src)),
                                getApplicationContext());
                    }
                })));
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
     * compiled into the app as a native library. Parsing the link and adding it to the catalog
     * (load, parse and save of the whole catalog) run in the background; the message and the
     * profiles page follow on the main thread. The new profile is added but not activated.
     */
    private void importProfile(String raw) {
        if (raw == null || raw.trim().isEmpty()) {
            Toast.makeText(this, R.string.prof_bad, Toast.LENGTH_SHORT).show();
            return;
        }
        // One catalog job at a time (see busy); a second tap while importing does nothing.
        if (busy) {
            return;
        }
        busy = true;
        String link = raw.trim();
        Context app = getApplicationContext();
        Ui.bg(this, () -> {
            String json = Mobile.parseRoutes(link);
            if (json == null || json.isEmpty()) {
                return null;
            }
            return ProfileStore.importLink(app, json);
        }, (p, err) -> {
            busy = false;
            // The catalog changed on another thread: re-read it when next needed.
            profStale = true;
            if (err != null || p == null) {
                Toast.makeText(this, R.string.prof_bad, Toast.LENGTH_SHORT).show();
                return;
            }
            Toast.makeText(this, R.string.prof_added, Toast.LENGTH_SHORT).show();
            show("profiles");
        });
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
     * Downloads the zone's list in the background, then merges it, saves and redraws.
     * Network IO must stay off the main thread (Android throws NetworkOnMainThreadException and
     * the UI would freeze).
     *
     * <p>The background job never touches the screen's store, which the user may keep editing
     * meanwhile (and which a profile switch may replace): it only downloads the rows. Merging
     * them happens in {@link #mergePull}. Ui.bg drops the result when the screen has closed.
     */
    private void pull(SplitStore.Zone z) {
        if (z.repo == null || z.repo.isEmpty()) {
            Toast.makeText(this, R.string.no_url, Toast.LENGTH_SHORT).show();
            return;
        }
        String zid = z.id;
        String url = z.repo;
        Ui.bg(this, () -> SplitStore.fetch(url), (rows, err) -> {
            if (err != null || rows == null) {
                Toast.makeText(this, R.string.pull_fail, Toast.LENGTH_SHORT).show();
                return;
            }
            mergePull(zid, rows);
        });
    }

    /**
     * Merges downloaded rows into zone {@code zid} of the current store, as SplitStore.pullZone
     * would, without the slow part on the main thread. replaceRepo looks up every row among the
     * services, which takes long for big lists, so it runs in the background on copies of only
     * this zone's services; the main thread then swaps the zone's services for the merged ones.
     *
     * <p>If the lists changed while the merge ran (an edit, an undo, a profile switch), the
     * merged copies are out of date: the merge simply runs again from the current lists, so no
     * edit is lost.
     */
    private void mergePull(String zid, List<Bat.Row> rows) {
        SplitStore base = store;
        long g = gen;
        // Copies of the zone's services, so the background merge cannot change live objects.
        List<SplitStore.Svc> mine = new ArrayList<>();
        for (SplitStore.Svc s : store.services) {
            if (zid.equals(s.zone)) {
                mine.add(copySvc(s));
            }
        }
        Ui.bg(this, () -> {
            SplitStore tmp = new SplitStore();
            tmp.services.addAll(mine);
            tmp.replaceRepo(zid, rows);
            return tmp.services;
        }, (merged, err) -> {
            if (err != null || merged == null) {
                Toast.makeText(this, R.string.pull_fail, Toast.LENGTH_SHORT).show();
                return;
            }
            if (store != base || gen != g) {
                mergePull(zid, rows);
                return;
            }
            SplitStore.Zone z = store.zone(zid);
            // The zone was deleted, or the operator's read-only lists became active: nothing to
            // apply. The download itself worked, so the usual message still shows.
            if (z != null && !ro()) {
                // Swap: drop the zone's services, add the merged ones. Inside the zone the order
                // is the one replaceRepo produced; other zones are untouched.
                java.util.Iterator<SplitStore.Svc> it = store.services.iterator();
                while (it.hasNext()) {
                    if (zid.equals(it.next().zone)) {
                        it.remove();
                    }
                }
                store.services.addAll(merged);
                z.pulled = System.currentTimeMillis();
                persist();
            }
            Toast.makeText(this, R.string.pulled, Toast.LENGTH_SHORT).show();
            show("list");
        });
    }

    /** Copy of a service with its own prefix and domain lists; the id is kept. */
    private static SplitStore.Svc copySvc(SplitStore.Svc s) {
        SplitStore.Svc n = new SplitStore.Svc();
        n.id = s.id;
        n.name = s.name;
        n.zone = s.zone;
        n.src = s.src;
        n.ignore = s.ignore;
        n.cidrs.addAll(s.cidrs);
        n.domains.addAll(s.domains);
        return n;
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

    /** Hides the snackbar and forgets the undo snapshot. (onDestroy also drops the 5-second timer.) */
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
        // Decided now, from the page that opened the picker.
        boolean forProfile = "profimp".equals(page);
        Uri uri = data.getData();
        // The file is read (at most PULL_MAX bytes) and parsed in the background; reading a
        // file from a cloud provider, or parsing a big list, froze the screen on the main
        // thread. Any error is shown as a toast with its message, as before.
        Ui.bg(this, () -> {
            try (InputStream in = getContentResolver().openInputStream(uri)) {
                if (in == null) {
                    return null;
                }
                String body = read(in);
                if (forProfile) {
                    return new Picked(body, "", null);
                }
                return new Picked(body, Bat.zoneHint(body), Bat.parse(body));
            }
        }, (r, err) -> {
            if (err != null) {
                Toast.makeText(this, err.getMessage(), Toast.LENGTH_SHORT).show();
                return;
            }
            if (r == null) {
                return;
            }
            if (forProfile) {
                importProfile(r.body);
                return;
            }
            pendingHint = r.hint;
            pendingRows = r.rows;
            show("imp");
        });
    }

    /** What a picked file gave: its text and, for a .bat list, the zone hint and the rows. */
    private static final class Picked {
        /** The whole file as text. */
        final String body;
        /** Zone named inside a .bat file ("REM zone: id"), or "". */
        final String hint;
        /** Rows of a .bat file; null for a route profile. */
        final List<Bat.Row> rows;

        Picked(String body, String hint, List<Bat.Row> rows) {
            this.body = body;
            this.hint = hint;
            this.rows = rows;
        }
    }

    /**
     * Reads a whole stream as UTF-8 text. Fails past SplitStore.PULL_MAX bytes (the same limit
     * as a download), so picking a huge file cannot exhaust memory.
     */
    private static String read(InputStream in) throws Exception {
        ByteArrayOutputStream o = new ByteArrayOutputStream();
        byte[] b = new byte[4096];
        int n;
        int total = 0;
        while ((n = in.read(b)) > 0) {
            total += n;
            if (total > SplitStore.PULL_MAX) {
                throw new IOException("list too large");
            }
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
     * callbacks; this keeps only afterTextChanged so callers can pass a lambda. It has no delay
     * of its own: callers that do slow work delay it themselves (search, persistSoon).
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
