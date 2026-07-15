/*
 * Copyright (C) 2026 The Android Open Source Project
 * Licensed under the Apache License, Version 2.0 (the "License");
 */
package androidx.media3.decoder.av3a;

import androidx.annotation.Nullable;
import androidx.media3.common.C;
import androidx.media3.common.util.UnstableApi;
import androidx.media3.common.util.Util;
import androidx.media3.decoder.DecoderInputBuffer;
import androidx.media3.decoder.SimpleDecoder;
import androidx.media3.decoder.SimpleDecoderOutputBuffer;
import java.nio.ByteBuffer;

/** Direct AVS3/AV3A decoder. No FFmpeg APIs are used by this class. */
@UnstableApi
public final class Av3aDecoder
    extends SimpleDecoder<DecoderInputBuffer, SimpleDecoderOutputBuffer, Av3aDecoderException> {

  private static final int OUTPUT_BUFFER_SIZE = 64 * 1024;
  private static final int ERROR_INVALID_DATA = -1;
  private static final int ERROR_OTHER = -2;

  private long nativeContext;
  private int channelCount;
  private int sampleRate;

  public Av3aDecoder(int numBuffers, int initialInputBufferSize, int channelCount, int sampleRate) throws Av3aDecoderException {
    super(new DecoderInputBuffer[numBuffers], new SimpleDecoderOutputBuffer[numBuffers]);
    if (!Av3aLibrary.isAvailable()) {
      throw new Av3aDecoderException("Failed to load libav3aJNI.so");
    }
    this.channelCount = channelCount;
    this.sampleRate = sampleRate;
    nativeContext = av3aInit();
    if (nativeContext == 0) {
      throw new Av3aDecoderException("Failed to initialize AV3A decoder");
    }
    setInitialInputBufferSize(initialInputBufferSize);
  }

  @Override
  public String getName() {
    return "libav3a-" + Av3aLibrary.getVersion();
  }

  @Override
  protected DecoderInputBuffer createInputBuffer() {
    return new DecoderInputBuffer(DecoderInputBuffer.BUFFER_REPLACEMENT_MODE_DIRECT);
  }

  @Override
  protected SimpleDecoderOutputBuffer createOutputBuffer() {
    return new SimpleDecoderOutputBuffer(this::releaseOutputBuffer);
  }

  @Override
  protected Av3aDecoderException createUnexpectedDecodeException(Throwable error) {
    return new Av3aDecoderException("Unexpected AV3A decode error", error);
  }

  @Override
  @Nullable
  protected Av3aDecoderException decode(
      DecoderInputBuffer inputBuffer, SimpleDecoderOutputBuffer outputBuffer, boolean reset) {
    if (reset && !av3aReset(nativeContext)) {
      return new Av3aDecoderException("Failed to reset AV3A decoder");
    }
    ByteBuffer inputData = Util.castNonNull(inputBuffer.data);
    ByteBuffer outputData = outputBuffer.init(inputBuffer.timeUs, OUTPUT_BUFFER_SIZE);
    int result =
        av3aDecode(
            nativeContext,
            inputData,
            inputData.limit(),
            outputData,
            OUTPUT_BUFFER_SIZE);
    android.util.Log.e("Av3aDebug", "av3aDecode input size=" + inputData.limit() + ", result=" + result);
    if (result == ERROR_OTHER) {
      android.util.Log.e("Av3aDebug", "av3aDecode returned ERROR_OTHER!");
      return new Av3aDecoderException("AV3A native decode failed; see logcat");
    }
    if (result == ERROR_INVALID_DATA || result == 0) {
      android.util.Log.e("Av3aDebug", "av3aDecode returned ERROR_INVALID_DATA or 0!");
      outputBuffer.shouldBeSkipped = true;
      return null;
    }
    // WORKAROUND: Attenuate the PCM volume by 50% (-6dB) to prevent Android AudioTrack's
    // 5.1-to-Stereo downmixer from clipping the audio and causing "electric static" noise.
    // Downmixing sums channels (L + 0.707*C + 0.5*Ls), which easily exceeds 16-bit limits.
    outputData.order(java.nio.ByteOrder.LITTLE_ENDIAN);
    for (int i = 0; i < result; i += 2) {
      short sample = outputData.getShort(i);
      outputData.putShort(i, (short) (sample / 2));
    }

    outputData.position(0);
    outputData.limit(result);
    return null;
  }

  @Override
  public void release() {
    super.release();
    av3aRelease(nativeContext);
    nativeContext = 0;
  }

  public int getChannelCount() {
    return channelCount;
  }

  public int getSampleRate() {
    return sampleRate;
  }

  public @C.PcmEncoding int getEncoding() {
    return C.ENCODING_PCM_16BIT;
  }

  private static native long av3aInit();

  private static native boolean av3aReset(long context);

  private static native int av3aDecode(
      long context, ByteBuffer inputData, int inputSize, ByteBuffer outputData, int outputSize);

  private static native int av3aGetChannelCount(long context);

  private static native int av3aGetSampleRate(long context);

  private static native void av3aRelease(long context);
}
