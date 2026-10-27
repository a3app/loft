package app.loft.android;

import android.app.AlertDialog;
import android.content.Intent;
import android.os.Bundle;
import android.os.Handler;
import android.os.Looper;
import android.text.TextUtils;
import android.view.Gravity;
import android.view.View;
import android.view.ViewGroup;
import android.widget.BaseAdapter;
import android.widget.Button;
import android.widget.EditText;
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

/**
 * Editor for the split-routing lists (SplitStore). Pages: zone list,
 * exclusions, zones, one zone, one service, repository, catalog,
 * import and export. Back returns to the list page first.
 */
public final class ListsActivity extends LoftActivity {
    private static final int REQ_IN = 8;
    private SplitStore store;
    private String zoneId = SplitStore.GLOBAL;
    private String page = "list";
    private String editing;
    private String catalogZone = SplitStore.GLOBAL;
    private String catalogBack = "repo";
    private final Handler main = new Handler(Looper.getMainLooper());
    private Set<String> sel;
    private SplitStore.Snap undo;
    private RevealFrame openSwipe;
    private List<Bat.Row> pendingRows;
    private String pendingHint = "";
    private final Runnable hideSnack = this::hideSnack;

    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        setContentView(R.layout.activity_lists);
        store = SplitStore.load(this);
        ToggleView split = findViewById(R.id.sw_split);
        split.setChecked(Prefs.split(this));
        split.setOnCheckedChangeListener((v, on) ->
                Prefs.p(this).edit().putBoolean("split", on).apply());
        findViewById(R.id.btn_back).setOnClickListener(v -> onBackPressed());
        findViewById(R.id.btn_more).setOnClickListener(this::openMore);
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
        if (!"list".equals(page)) {
            show("list");
            return;
        }
        super.onBackPressed();
    }

    private void persist() {
        store.save(this);
    }

    private void openMore(View v) {
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

    private void show(String p) {
        page = p;
        boolean list = "list".equals(p);
        boolean selecting = list && sel != null;
        findViewById(R.id.head).setVisibility(selecting ? View.GONE : View.VISIBLE);
        findViewById(R.id.selbar).setVisibility(selecting ? View.VISIBLE : View.GONE);
        findViewById(R.id.sw_split).setVisibility(list && !selecting ? View.VISIBLE : View.GONE);
        findViewById(R.id.btn_more).setVisibility(list && !selecting ? View.VISIBLE : View.GONE);
        if (selecting) {
            int n = sel.size();
            ((TextView) findViewById(R.id.sel_title)).setText(getString(R.string.split_sel_n, n));
            findViewById(R.id.btn_sel_del).setEnabled(n > 0);
        }
        TextView title = findViewById(R.id.title);
        ViewGroup body = findViewById(R.id.body);
        body.removeAllViews();
        View content;
        switch (p) {
            case "zones":
                title.setText(R.string.zones);
                content = zonesPage();
                break;
            case "zone":
                title.setText(R.string.zone);
                content = zonePage();
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
            default:
                title.setText(R.string.split);
                content = listPage();
                break;
        }
        body.addView(content, new ViewGroup.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT));
    }

    private View listPage() {
        LinearLayout root = col();
        root.addView(subline(Prefs.split(this)
                ? getString(R.string.split_sub) : getString(R.string.split_sub_off)));
        LinearLayout zhead = row();
        zhead.addView(label(getString(R.string.zone)),
                new LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1));
        zhead.addView(ghostSm(getString(R.string.zones), v -> show("zones")));
        root.addView(zhead);
        LinearLayout chips = new LinearLayout(this);
        chips.setOrientation(LinearLayout.HORIZONTAL);
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
        EditText search = field("");
        search.setHint(getString(R.string.search));
        search.setInputType(android.text.InputType.TYPE_CLASS_TEXT);
        root.addView(search);
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
        if (sel == null) {
            LinearLayout tail = col(false);
            int nx = store.exclCount(zoneId);
            tail.addView(clickRow(getString(R.string.split_excl), nx + " ›",
                    v -> show("excl")));
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
            lv.addFooterView(tail, null, false);
        }
        lv.setAdapter(adapter);
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
        return root;
    }

    private void bindSvcRow(RevealFrame row, SplitStore.Svc s, boolean excl) {
        row.settleNow(0);
        TextView name = row.findViewById(R.id.name);
        TextView sub = row.findViewById(R.id.sub);
        TextView meta = row.findViewById(R.id.meta);
        TextView src = row.findViewById(R.id.src);
        TextView ck = row.findViewById(R.id.ck);
        TextView act = row.findViewById(R.id.act_del);
        name.setText(excl ? SplitStore.join(s.cidrs) : s.name);
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
            src.setText("repo".equals(s.src) ? getString(R.string.from_repo) : getString(R.string.mine));
            act.setText(R.string.delete);
        }
        boolean picking = sel != null && !excl;
        ck.setVisibility(picking ? View.VISIBLE : View.GONE);
        if (picking) {
            boolean on = sel.contains(s.id);
            ck.setBackgroundResource(on ? R.drawable.bg_ck_on : R.drawable.bg_ck);
            ck.setText(on ? "✓" : "");
            row.lock();
        } else {
            row.leftOnly();
        }
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
        card.setOnLongClickListener(v -> {
            if (excl || sel != null) {
                return false;
            }
            sel = new HashSet<>();
            sel.add(s.id);
            show("list");
            return true;
        });
        act.setOnClickListener(v -> gone(java.util.Collections.singletonList(s.id)));
    }

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

    private View svcPage() {
        SplitStore.Svc s = store.svc(editing);
        if (s == null) {
            s = new SplitStore.Svc();
            s.zone = zoneId;
            store.services.add(s);
            editing = s.id;
        }
        SplitStore.Svc svc = s;
        LinearLayout root = col();
        root.addView(subline(svc.ignore
                ? getString(R.string.split_add_body_ignore) : getString(R.string.split_add_body)));
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
        del.setOnClickListener(v -> {
            String id = svc.id;
            show("list");
            gone(java.util.Collections.singletonList(id));
        });
        root.addView(del);
        return scroll(root);
    }

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
        return scroll(root);
    }

    private String dirLabel(SplitStore.Zone z) {
        if (z.block) {
            return getString(R.string.split_blocked);
        }
        return getString(z.through ? R.string.through_loft : R.string.around_loft);
    }

    private View zonePage() {
        SplitStore.Zone z = store.zone(editing);
        if (z == null) {
            show("zones");
            return col();
        }
        LinearLayout root = col();
        EditText name = field(z.name);
        name.setEnabled(!z.builtin);
        root.addView(label(getString(R.string.zone_name)));
        root.addView(name);
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
        EditText url = urlField(z.repo);
        root.addView(urlRow(url, v -> {
            z.repo = url.getText().toString().trim();
            persist();
            openCatalog(z.id, "zone");
        }));
        root.addView(autoRow(z));
        root.addView(hint(z.pulled == 0 ? getString(R.string.no_list) : getString(R.string.pulled)));
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

    private View repoPage() {
        SplitStore.Zone z = store.zone(zoneId);
        LinearLayout root = col();
        if (z == null) {
            return root;
        }
        root.addView(subline(z.name + " · " + getString(R.string.repo_d)));
        root.addView(label(getString(R.string.list_url)));
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

    private View impPage() {
        LinearLayout root = col();
        root.addView(subline(getString(R.string.split_import_body)));
        if (pendingRows == null) {
            root.addView(ghostSm(getString(R.string.from_file), v -> {
                Intent i = new Intent(Intent.ACTION_OPEN_DOCUMENT);
                i.addCategory(Intent.CATEGORY_OPENABLE);
                i.setType("*/*");
                startActivityForResult(i, REQ_IN);
            }));
            return scroll(root);
        }
        root.addView(hint(getString(R.string.split_import_detect, pendingRows.size())));
        root.addView(label(getString(R.string.zone)));
        LinearLayout zseg = new LinearLayout(this);
        zseg.setOrientation(LinearLayout.HORIZONTAL);
        int zi = 0;
        String dest = pendingHint;
        if (store.zone(dest) == null) {
            dest = zoneId;
        }
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

    private void openCatalog(String zone, String back) {
        catalogZone = zone;
        catalogBack = back;
        show("catalog");
    }

    private View catalogPage() {
        LinearLayout root = col();
        root.addView(subline(getString(R.string.catalog_d)));
        String lang = Prefs.applyLang(this);
        SplitStore.Zone cur = store.zone(catalogZone);
        String current = cur != null ? cur.repo : "";
        for (String[] row : SplitStore.CATALOG) {
            String id = row[0];
            String url = SplitStore.catalogUrl(id);
            LinearLayout card = new LinearLayout(this);
            card.setOrientation(LinearLayout.HORIZONTAL);
            card.setGravity(Gravity.CENTER_VERTICAL);
            card.setBackgroundResource(url.equals(current) ? R.drawable.bg_card_live : R.drawable.bg_card);
            card.setPadding(dp(12), dp(9), dp(12), dp(9));
            LinearLayout.LayoutParams lp = new LinearLayout.LayoutParams(
                    ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT);
            lp.bottomMargin = dp(6);
            card.setLayoutParams(lp);
            LinearLayout grow = col(false);
            TextView n = new TextView(this);
            n.setText(SplitStore.catalogLabel(id, lang));
            n.setTextColor(getColor(R.color.snow));
            n.setTextSize(13.5f);
            n.getPaint().setFakeBoldText(true);
            TextView m = new TextView(this);
            m.setText(id + ".bat");
            m.setTextColor(getColor(R.color.mute));
            m.setTextSize(10.5f);
            m.setTypeface(android.graphics.Typeface.MONOSPACE);
            m.setPadding(0, dp(2), 0, 0);
            grow.addView(n);
            grow.addView(m);
            card.addView(grow, new LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1));
            card.addView(meta(getString(R.string.from_repo)));
            card.setOnClickListener(v -> {
                SplitStore.Zone z = store.zone(catalogZone);
                if (z != null) {
                    z.repo = url;
                    persist();
                }
                show(catalogBack);
            });
            root.addView(card);
        }
        return scroll(root);
    }

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

    private void gone(List<String> ids) {
        SplitStore.Snap snap = store.snap();
        SplitStore.Gone g = store.removeSvcs(ids);
        if (g.n == 0) {
            return;
        }
        persist();
        undo = snap;
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
        show("list".equals(page) || "excl".equals(page) ? page : "list");
    }

    private void openClear(boolean excl) {
        SplitStore.Zone z = store.zone(zoneId);
        String name = z == null ? zoneId : z.name;
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

    private void doClear(String how, String name) {
        SplitStore.Snap snap = store.snap();
        store.clearZone(zoneId, how);
        persist();
        undo = snap;
        snack(getString(R.string.split_gone_clear, name));
        show("list");
    }

    private void snack(String text) {
        TextView t = findViewById(R.id.snack_t);
        t.setText(text);
        findViewById(R.id.snack).setVisibility(View.VISIBLE);
        main.removeCallbacks(hideSnack);
        main.postDelayed(hideSnack, 5000);
    }

    private void hideSnack() {
        findViewById(R.id.snack).setVisibility(View.GONE);
        main.removeCallbacks(hideSnack);
        undo = null;
    }

    @Override
    protected void onActivityResult(int requestCode, int resultCode, Intent data) {
        super.onActivityResult(requestCode, resultCode, data);
        if (requestCode != REQ_IN || resultCode != RESULT_OK || data == null || data.getData() == null) {
            return;
        }
        try (InputStream in = getContentResolver().openInputStream(data.getData())) {
            if (in == null) {
                return;
            }
            String body = read(in);
            pendingHint = Bat.zoneHint(body);
            pendingRows = Bat.parse(body);
            show("imp");
        } catch (Exception e) {
            Toast.makeText(this, e.getMessage(), Toast.LENGTH_SHORT).show();
        }
    }

    private static String read(InputStream in) throws Exception {
        ByteArrayOutputStream o = new ByteArrayOutputStream();
        byte[] b = new byte[4096];
        int n;
        while ((n = in.read(b)) > 0) {
            o.write(b, 0, n);
        }
        return o.toString(StandardCharsets.UTF_8.name());
    }

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

    private LinearLayout col() {
        return col(true);
    }

    private LinearLayout col(boolean pad) {
        LinearLayout l = new LinearLayout(this);
        l.setOrientation(LinearLayout.VERTICAL);
        if (pad) {
            int p = dp(16);
            l.setPadding(p, p / 2, p, p);
        }
        return l;
    }

    private LinearLayout row() {
        LinearLayout l = new LinearLayout(this);
        l.setOrientation(LinearLayout.HORIZONTAL);
        l.setGravity(Gravity.CENTER_VERTICAL);
        return l;
    }

    private ScrollView scroll(View v) {
        ScrollView s = new ScrollView(this);
        s.addView(v);
        return s;
    }

    private Button inflateBtn(int layout) {
        return (Button) getLayoutInflater().inflate(layout, findViewById(R.id.body), false);
    }

    private LinearLayout.LayoutParams stack(int top) {
        LinearLayout.LayoutParams lp = new LinearLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT);
        lp.topMargin = dp(top);
        return lp;
    }

    private Button segBtn(String t) {
        Button b = inflateBtn(R.layout.btn_seg);
        b.setText(t);
        b.setLayoutParams(new LinearLayout.LayoutParams(0, dp(32), 1));
        return b;
    }

    private Button chip(String t) {
        Button b = inflateBtn(R.layout.btn_chip);
        b.setText(t);
        LinearLayout.LayoutParams lp = new LinearLayout.LayoutParams(
                ViewGroup.LayoutParams.WRAP_CONTENT, ViewGroup.LayoutParams.WRAP_CONTENT);
        lp.rightMargin = dp(6);
        b.setLayoutParams(lp);
        return b;
    }

    private Button pill(String t) {
        Button b = chip(t);
        if (b instanceof MaterialButton) {
            ((MaterialButton) b).setCornerRadius(dp(18));
        }
        return b;
    }

    private Button go(String t, int top) {
        Button b = inflateBtn(R.layout.btn_go);
        b.setText(t);
        b.setLayoutParams(stack(top));
        return b;
    }

    private Button danger(String t, int top) {
        Button b = inflateBtn(R.layout.btn_danger);
        b.setText(t);
        b.setLayoutParams(stack(top));
        return b;
    }

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

    private Button mini(String t) {
        Button b = inflateBtn(R.layout.btn_mini);
        b.setText(t);
        return b;
    }

    private Button mini(String t, View.OnClickListener l) {
        Button b = mini(t);
        b.setOnClickListener(l);
        return b;
    }

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

    private EditText urlField(String v) {
        EditText e = field(v);
        e.setSingleLine(true);
        e.setEllipsize(TextUtils.TruncateAt.END);
        e.setTypeface(android.graphics.Typeface.MONOSPACE);
        e.setTextSize(12);
        e.setLayoutParams(new LinearLayout.LayoutParams(0, dp(40), 1));
        return e;
    }

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

    private TextView label(String t) {
        TextView v = new TextView(this);
        v.setText(t);
        v.setTextColor(getColor(R.color.mute));
        v.setTextSize(12);
        return v;
    }

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

    private TextView meta(String t) {
        TextView v = new TextView(this);
        v.setText(t);
        v.setTextColor(getColor(R.color.stone));
        v.setTextSize(11);
        return v;
    }

    private TextView snow(String t) {
        TextView v = new TextView(this);
        v.setText(t);
        v.setTextColor(getColor(R.color.snow));
        return v;
    }

    private LinearLayout.LayoutParams weight(int left) {
        LinearLayout.LayoutParams lp = new LinearLayout.LayoutParams(
                0, ViewGroup.LayoutParams.WRAP_CONTENT, 1);
        lp.leftMargin = dp(left);
        return lp;
    }

    private int dp(int v) {
        return Math.round(v * getResources().getDisplayMetrics().density);
    }

    private static final class SimpleWatch implements android.text.TextWatcher {
        private final Runnable r;

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
