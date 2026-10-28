/*
 * Android client, shared code: the log screen used by both phone and TV
 * builds. It shows the text kept by Journal and lets the user copy, share
 * or clear it.
 */
package app.loft.android;

import android.app.AlertDialog;
import android.content.ClipData;
import android.content.ClipboardManager;
import android.content.Intent;
import android.os.Bundle;
import android.text.Editable;
import android.widget.TextView;

import java.util.ArrayDeque;
import java.util.List;

/**
 * Shows the session journal (Journal) with copy, share and clear. Listens to
 * Bus while visible, but most Bus events are about traffic, not the log, so
 * it redraws only when Journal.version changed. Journal keeps at most 400
 * lines in memory; a redraw appends the new lines and cuts the ones that fell
 * off the front, instead of replacing all 400 lines (re-laying out that much
 * text on every event froze slower devices). The journal text is already
 * redacted by Journal before it is stored, so copy and share pass it on as is.
 */
public final class LogActivity extends LoftActivity implements Bus.Listener {
    /** The text view holding the whole journal. */
    private TextView body;
    /** Journal.version of the text now on screen; -1 forces a full draw. */
    private long shownVersion = -1;
    /** Journal clear() count of the text on screen; a change means "start over". */
    private long shownClears = -1;
    /** Journal line number (see Journal.Snap.first) of the first line on screen. */
    private long shownFirst;
    /**
     * Length in characters (newline included) of each line on screen, oldest
     * first. Needed to cut whole lines off the front: a line may itself
     * contain newlines (multi-line error text), so counting '\n' would not work.
     */
    private final ArrayDeque<Integer> shownLens = new ArrayDeque<>();

    /** Inflates the screen and wires back, copy, share and clear (clear asks first). */
    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        setContentView(R.layout.activity_log);
        body = findViewById(R.id.body);
        findViewById(R.id.btn_back).setOnClickListener(v -> finish());
        findViewById(R.id.btn_copy).setOnClickListener(v -> {
            ClipboardManager cm = (ClipboardManager) getSystemService(CLIPBOARD_SERVICE);
            if (cm != null) {
                cm.setPrimaryClip(ClipData.newPlainText("loft", Journal.text()));
            }
        });
        findViewById(R.id.btn_share).setOnClickListener(v -> {
            Intent i = new Intent(Intent.ACTION_SEND);
            i.setType("text/plain");
            i.putExtra(Intent.EXTRA_TEXT, Journal.text());
            startActivity(Intent.createChooser(i, getString(R.string.share)));
        });
        findViewById(R.id.btn_clear).setOnClickListener(v ->
                new AlertDialog.Builder(this)
                        .setMessage(R.string.confirm_clear)
                        .setPositiveButton(R.string.clear_log, (d, w) -> Journal.clear())
                        .setNegativeButton(R.string.cancel, null)
                        .show());
        paint();
    }

    /** Starts listening for new log lines and shows the current text. */
    @Override
    protected void onResume() {
        super.onResume();
        Bus.watch(this);
        paint();
    }

    /** Stops listening while the screen is hidden. */
    @Override
    protected void onPause() {
        Bus.drop(this);
        super.onPause();
    }

    /** Bus event: the journal (or other state) changed, so redraw. */
    @Override
    public void onLoft() {
        paint();
    }

    /**
     * Brings the text on screen up to date with the journal. Does nothing when
     * the journal has not changed since the last draw. Otherwise it edits the
     * shown text in place: cuts lines that the journal dropped (it keeps 400)
     * and appends lines added since. The text is kept as an Editable so those
     * edits only re-lay out the changed part. The end result is the same text
     * Journal.text() would give. After clear(), or if the screen fell too far
     * behind to tell what changed, the whole text is set again.
     */
    private void paint() {
        // 1. Cheap check without a lock or a copy: nothing new, nothing to do.
        if (Journal.version == shownVersion) {
            return;
        }
        Journal.Snap s = Journal.snap();
        long shownEnd = shownFirst + shownLens.size();
        Editable ed = body.getEditableText();
        // 2. Start over when the journal was cleared, on the first draw, when
        //    lines we never showed were already dropped (a gap), or when the
        //    view's text is not our Editable (should not happen).
        if (ed == null || s.clears != shownClears || s.first > shownEnd || s.first < shownFirst) {
            StringBuilder b = new StringBuilder();
            shownLens.clear();
            for (String l : s.lines) {
                b.append(l).append('\n');
                shownLens.addLast(l.length() + 1);
            }
            body.setText(b, TextView.BufferType.EDITABLE);
            shownFirst = s.first;
        } else {
            // 3. Cut lines from the front that the journal no longer keeps.
            int cut = 0;
            while (shownFirst < s.first && !shownLens.isEmpty()) {
                cut += shownLens.removeFirst();
                shownFirst++;
            }
            if (cut > 0) {
                ed.delete(0, Math.min(cut, ed.length()));
            }
            // 4. Append the lines that are newer than the last one on screen.
            List<String> lines = s.lines;
            int from = (int) (shownEnd - s.first);
            if (from < lines.size()) {
                StringBuilder b = new StringBuilder();
                for (int i = from; i < lines.size(); i++) {
                    String l = lines.get(i);
                    b.append(l).append('\n');
                    shownLens.addLast(l.length() + 1);
                }
                ed.append(b);
            }
        }
        shownVersion = s.version;
        shownClears = s.clears;
    }
}
