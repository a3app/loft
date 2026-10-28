/*
 * Android client, shared code: the base class for every screen (activity)
 * in the phone and TV builds. Its only job is to apply the in-app language
 * choice from Lang before any layout is inflated.
 */
package app.loft.android;

import android.content.Context;

import androidx.appcompat.app.AppCompatActivity;

/**
 * Base class for the app's screens. Applies the Prefs language before
 * inflate so XML layouts and getString() share one locale. Screens extend
 * this instead of AppCompatActivity directly.
 */
public abstract class LoftActivity extends AppCompatActivity {
    /** Swaps in a context with the chosen locale; runs before onCreate(), so every string resource follows it. */
    @Override
    protected void attachBaseContext(Context newBase) {
        super.attachBaseContext(Lang.wrap(newBase));
    }
}
