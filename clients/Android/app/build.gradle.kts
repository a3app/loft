plugins {
    id("com.android.application")
}

val androidDist = rootProject.projectDir.resolve("../../dist/clients/Android").canonicalFile

layout.buildDirectory.set(androidDist.resolve("gradle/app"))

android {
    namespace = "app.loft.android"
    compileSdk = 36
    defaultConfig {
        applicationId = "app.loft.android"
        minSdk = 26
        targetSdk = 36
        versionCode = 26
        versionName = "1.0.26"
    }
    buildTypes {
        release {
            isMinifyEnabled = false
        }
    }
    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
    packaging {
        jniLibs {
            useLegacyPackaging = true
        }
    }
}

dependencies {
    implementation(files("../../mobile/build/engine.aar"))
    implementation("androidx.appcompat:appcompat:1.7.0")
    implementation("com.google.android.material:material:1.12.0")
    implementation("androidx.camera:camera-core:1.4.1")
    implementation("androidx.camera:camera-camera2:1.4.1")
    implementation("androidx.camera:camera-lifecycle:1.4.1")
    implementation("androidx.camera:camera-view:1.4.1")
    implementation("com.google.zxing:core:3.5.3")
    testImplementation("junit:junit:4.13.2")
}

fun packEmulatorApp(apkDir: File, versionName: String, versionCode: Int) {
    val src = rootProject.projectDir.resolve("emulator")
    val script = src.resolve("loft")
    val plistIn = src.resolve("Info.plist")
    require(script.isFile) { "missing ${script}" }
    require(plistIn.isFile) { "missing ${plistIn}" }
    val bundle = apkDir.resolve("loft.app")
    if (bundle.exists()) bundle.deleteRecursively()
    val contents = bundle.resolve("Contents")
    val macos = contents.resolve("MacOS")
    val resources = contents.resolve("Resources")
    macos.mkdirs()
    resources.mkdirs()
    script.copyTo(macos.resolve("loft"))
    macos.resolve("loft").setExecutable(true, false)
    val plist = plistIn.readText()
        .replace("__LOFT_VERSION__", versionName)
        .replace("__LOFT_CODE__", versionCode.toString())
    contents.resolve("Info.plist").writeText(plist)
    contents.resolve("PkgInfo").writeText("APPL????")
    val icns = rootProject.projectDir.resolve("../macOS/Resources/loft.icns")
    if (icns.isFile) icns.copyTo(resources.resolve("loft.icns"))
    try {
        ProcessBuilder("xattr", "-cr", bundle.absolutePath).start().waitFor()
    } catch (_: Exception) {
    }
}

afterEvaluate {
    val apk = layout.buildDirectory.file("outputs/apk/debug/app-debug.apk")
    tasks.register("distDebug") {
        dependsOn("packageDebug")
        inputs.file(apk)
        inputs.dir(rootProject.projectDir.resolve("emulator"))
        outputs.file(androidDist.resolve("app-debug.apk"))
        outputs.dir(androidDist.resolve("loft.app"))
        doLast {
            androidDist.mkdirs()
            apk.get().asFile.copyTo(androidDist.resolve("app-debug.apk"), overwrite = true)
            packEmulatorApp(
                androidDist,
                android.defaultConfig.versionName!!,
                android.defaultConfig.versionCode!!,
            )
        }
    }
    tasks.register("packageEmulatorApp") {
        dependsOn("distDebug")
    }
    tasks.named("assembleDebug") {
        finalizedBy("distDebug")
    }
}
