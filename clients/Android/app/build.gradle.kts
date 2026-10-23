plugins {
    id("com.android.application")
}

val androidDist = rootProject.projectDir.resolve("../../dist/clients/Android").canonicalFile

layout.buildDirectory.set(androidDist.resolve("gradle/app"))

android {
    namespace = "app.loft.android"
    compileSdk = 36
    defaultConfig {
        minSdk = 26
        targetSdk = 36
        versionCode = 27
        versionName = "1.0.27"
    }
    flavorDimensions += "device"
    productFlavors {
        create("phone") {
            dimension = "device"
            isDefault = true
            applicationId = "app.loft.android"
        }
        create("tv") {
            dimension = "device"
            applicationId = "app.loft.android.tv"
        }
    }
    signingConfigs {
        create("release") {
            val store = System.getenv("LOFT_STORE_FILE")
            if (!store.isNullOrBlank() && file(store).isFile) {
                storeFile = file(store)
                storePassword = System.getenv("LOFT_STORE_PASSWORD") ?: ""
                keyAlias = System.getenv("LOFT_KEY_ALIAS") ?: "loft"
                keyPassword = System.getenv("LOFT_KEY_PASSWORD") ?: storePassword
            }
        }
    }
    buildTypes {
        release {
            isMinifyEnabled = false
            val rel = signingConfigs.getByName("release")
            if (rel.storeFile != null) {
                signingConfig = rel
            }
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
    implementation("androidx.recyclerview:recyclerview:1.3.2")
    implementation("com.google.android.material:material:1.12.0")
    implementation("com.google.zxing:core:3.5.3")
    add("phoneImplementation", "androidx.camera:camera-core:1.4.1")
    add("phoneImplementation", "androidx.camera:camera-camera2:1.4.1")
    add("phoneImplementation", "androidx.camera:camera-lifecycle:1.4.1")
    add("phoneImplementation", "androidx.camera:camera-view:1.4.1")
    add("tvImplementation", "androidx.leanback:leanback:1.2.0")
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
    val phoneApk = layout.buildDirectory.file("outputs/apk/phone/debug/app-phone-debug.apk")
    val tvApk = layout.buildDirectory.file("outputs/apk/tv/debug/app-tv-debug.apk")
    tasks.register("distDebug") {
        dependsOn("packagePhoneDebug", "packageTvDebug")
        inputs.file(phoneApk)
        inputs.file(tvApk)
        inputs.dir(rootProject.projectDir.resolve("emulator"))
        outputs.file(androidDist.resolve("app-debug.apk"))
        outputs.file(androidDist.resolve("app-tv-debug.apk"))
        outputs.dir(androidDist.resolve("loft.app"))
        doLast {
            androidDist.mkdirs()
            phoneApk.get().asFile.copyTo(androidDist.resolve("app-debug.apk"), overwrite = true)
            tvApk.get().asFile.copyTo(androidDist.resolve("app-tv-debug.apk"), overwrite = true)
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
    tasks.named("assemblePhoneDebug") {
        finalizedBy("distDebug")
    }
    tasks.named("assembleTvDebug") {
        finalizedBy("distDebug")
    }
}
