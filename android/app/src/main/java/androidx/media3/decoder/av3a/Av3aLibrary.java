/*
 * Copyright (C) 2026 The Android Open Source Project
 * Licensed under the Apache License, Version 2.0 (the "License");
 */
package androidx.media3.decoder.av3a;

import androidx.annotation.Nullable;
import androidx.media3.common.MediaLibraryInfo;
import androidx.media3.common.util.LibraryLoader;
import androidx.media3.common.util.UnstableApi;

/** Loads and identifies the dedicated AV3A decoder library. */
@UnstableApi
public final class Av3aLibrary {

  static {
    MediaLibraryInfo.registerModule("media3.decoder.av3a");
  }

  private static final LibraryLoader LOADER =
      new LibraryLoader("av3aJNI") {
        @Override
        protected void loadLibrary(String name) {
          // Android TV (older OS) requires explicit loading of libc++_shared
          System.loadLibrary("c++_shared");
          // av3a_binaural_render is loaded via dlopen in native code — no need to load here
          System.loadLibrary("AVS3AudioDec");
          System.loadLibrary(name);
        }
      };

  private Av3aLibrary() {}

  /** Returns whether the dedicated source-built decoder can be loaded. */
  public static boolean isAvailable() {
    return LOADER.isAvailable();
  }

  /**
   * Asynchronously pre-loads the native AV3A libraries in a background thread.
   *
   * <p>Call this early during app startup (e.g. {@link android.app.Application#onCreate()}) so
   * that the ~12 MB of native libraries (libc++_shared, libAVS3AudioDec, libav3aJNI) are ready
   * before the first AV3A stream is played. This eliminates the ~3-second loading delay on
   * ARM devices when the decoder is first needed.
   *
   * <p>This method is safe to call from any thread. Subsequent calls to {@link #isAvailable()}
   * from the main thread will block on the same {@code synchronized} section until the background
   * pre-load completes, guaranteeing that the library is either fully loaded or not at all.
   */
  public static void preloadAsync() {
    new Thread(
            () -> {
              try {
                isAvailable();
              } catch (Throwable ignored) {
                // Library not available on this platform — nothing to do
              }
            },
            "Av3aPreloader")
        .start();
  }

  /** Returns the native decoder version. */
  @Nullable
  public static String getVersion() {
    return isAvailable() ? av3aGetVersion() : null;
  }

  private static native String av3aGetVersion();
}
