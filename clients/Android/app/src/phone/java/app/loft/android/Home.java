/*
 * Android client, phone build: tells shared code which activity is the app's
 * home screen. The TV build has its own Home.java pointing at TvActivity, so
 * code in src/main (for example the VPN notification) can open the right
 * screen without knowing which build it is in.
 */
package app.loft.android;

/**
 * Build-specific pointer to the home screen, phone flavor. A "flavor" is a
 * Gradle build variant: src/phone and src/tv each have a class with this same
 * name, and only one of them is compiled into a given APK. Shared code calls
 * Home.activity() instead of naming MainActivity or TvActivity directly.
 */
final class Home {
    /**
     * Returns the activity to open from shared code, for example when the user
     * taps the VPN notification. On the phone build this is MainActivity.
     */
    static Class<?> activity() {
        return MainActivity.class;
    }
}
