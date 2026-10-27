/*
 * Android client, phone build: tells shared code which activity is the app's
 * home screen. The TV build has its own Home.java pointing at TvActivity, so
 * code in src/main (for example the VPN notification) can open the right
 * screen without knowing which build it is in.
 */
package app.loft.android;

/** Launcher activity for the phone flavor. */
final class Home {
    /** Returns the activity to open when the user taps the app's notification. */
    static Class<?> activity() {
        return MainActivity.class;
    }
}
