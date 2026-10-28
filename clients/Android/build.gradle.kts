plugins {
    id("com.android.application") version "9.4.1" apply false
}

layout.buildDirectory.set(
    rootProject.projectDir.resolve("../../dist/clients/Android/gradle/root").canonicalFile
)
