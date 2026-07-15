/*
 * Copyright (C) 2026 The Android Open Source Project
 * Licensed under the Apache License, Version 2.0 (the "License");
 */
package androidx.media3.decoder.av3a;

import android.os.Build;
import android.os.Handler;
import androidx.annotation.Nullable;
import androidx.media3.common.C;
import androidx.media3.common.Format;
import androidx.media3.common.MimeTypes;
import androidx.media3.common.util.Log;
import androidx.media3.common.util.TraceUtil;
import androidx.media3.common.util.UnstableApi;
import androidx.media3.common.util.Util;
import androidx.media3.decoder.CryptoConfig;
import androidx.media3.exoplayer.audio.AudioRendererEventListener;
import androidx.media3.exoplayer.audio.AudioSink;
import androidx.media3.exoplayer.audio.DecoderAudioRenderer;

/** Renderer that claims only {@code audio/av3a}; every other audio format stays on its old path. */
@UnstableApi
public final class Av3aAudioRenderer extends DecoderAudioRenderer<Av3aDecoder> {

  private static final String TAG = "Av3aAudioRenderer";
  private static final int NUM_BUFFERS = 16;
  private static final int DEFAULT_INPUT_BUFFER_SIZE = 4096 * 64;
  private static final int[] BED_5_1_MAPPING = {0, 1, 2, 3, 4, 5};

  public Av3aAudioRenderer(
      @Nullable Handler eventHandler,
      @Nullable AudioRendererEventListener eventListener,
      AudioSink audioSink) {
    super(eventHandler, eventListener, audioSink);
  }

  @Override
  public String getName() {
    return TAG;
  }

  @Override
  protected @C.FormatSupport int supportsFormatInternal(Format format) {
    if (!"audio/av3a".equals(format.sampleMimeType) || !Av3aLibrary.isAvailable()) {
      return C.FORMAT_UNSUPPORTED_TYPE;
    }
    if (format.cryptoType != C.CRYPTO_TYPE_NONE) {
      return C.FORMAT_UNSUPPORTED_DRM;
    }
    Format nativePcm =
        Util.getPcmFormat(C.ENCODING_PCM_16BIT, format.channelCount, format.sampleRate);
    if (sinkSupportsFormat(nativePcm)) {
      return C.FORMAT_HANDLED;
    }
    Format pcm7point1 = Util.getPcmFormat(C.ENCODING_PCM_16BIT, 8, format.sampleRate);
    if (format.channelCount >= 8 && sinkSupportsFormat(pcm7point1)) {
      return C.FORMAT_HANDLED;
    }
    Format bedPcm = Util.getPcmFormat(C.ENCODING_PCM_16BIT, 6, format.sampleRate);
    if (format.channelCount >= 6 && sinkSupportsFormat(bedPcm)) {
      return C.FORMAT_HANDLED;
    }
    return C.FORMAT_UNSUPPORTED_SUBTYPE;
  }

  @Override
  public @AdaptiveSupport int supportsMixedMimeTypeAdaptation() {
    return ADAPTIVE_NOT_SEAMLESS;
  }

  @Override
  protected Av3aDecoder createDecoder(Format format, @Nullable CryptoConfig cryptoConfig)
      throws Av3aDecoderException {
    TraceUtil.beginSection("createAv3aDecoder");
    int inputBufferSize =
        format.maxInputSize != Format.NO_VALUE ? format.maxInputSize : DEFAULT_INPUT_BUFFER_SIZE;
    int channelCount = format.channelCount != Format.NO_VALUE ? format.channelCount : 6;
    int sampleRate = format.sampleRate != Format.NO_VALUE ? format.sampleRate : 48000;
    Av3aDecoder decoder = new Av3aDecoder(NUM_BUFFERS, inputBufferSize, channelCount, sampleRate);
    TraceUtil.endSection();
    return decoder;
  }

  @Override
  protected Format getOutputFormat(Av3aDecoder decoder) {
    return Util.getPcmFormat(
        decoder.getEncoding(), decoder.getChannelCount(), decoder.getSampleRate());
  }

  @Override
  @Nullable
  protected int[] getChannelMapping(Av3aDecoder decoder) {
    int channelCount = decoder.getChannelCount();
    
    Format nativePcm =
        Util.getPcmFormat(
            decoder.getEncoding(), channelCount, decoder.getSampleRate());
    boolean nativelySupported = 
        Build.VERSION.SDK_INT >= 32 
            && getSinkFormatSupport(nativePcm) == AudioSink.SINK_FORMAT_SUPPORTED_DIRECTLY;

    if (nativelySupported) {
      Log.i(TAG, "AV3A PCM output: " + channelCount + " channels supported directly by hardware.");
      return null;
    }

    if (channelCount >= 8) {
      Format pcm7point1 = Util.getPcmFormat(decoder.getEncoding(), 8, decoder.getSampleRate());
      if (getSinkFormatSupport(pcm7point1) == AudioSink.SINK_FORMAT_SUPPORTED_DIRECTLY) {
        Log.i(TAG, "AV3A PCM output: hardware supports 7.1. Mapping " + channelCount + " -> 8 channels.");
        return new int[]{0, 1, 2, 3, 4, 5, 6, 7};
      }
    }

    if (channelCount >= 6) {
      Log.i(TAG, "AV3A PCM output: hardware falls back to 5.1. Mapping " + channelCount + " -> 6 channels.");
      return BED_5_1_MAPPING;
    }

    return null;
  }
}
