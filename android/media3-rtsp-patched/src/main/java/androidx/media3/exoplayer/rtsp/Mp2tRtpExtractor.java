/*
 * Copyright 2024 The Android Open Source Project
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *      http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package androidx.media3.exoplayer.rtsp;

import static com.google.common.base.Preconditions.checkNotNull;

import android.os.SystemClock;
import androidx.annotation.GuardedBy;
import androidx.annotation.Nullable;
import androidx.media3.common.C;
import androidx.media3.common.util.ParsableByteArray;
import androidx.media3.extractor.Extractor;
import androidx.media3.extractor.ExtractorInput;
import androidx.media3.extractor.ExtractorOutput;
import androidx.media3.extractor.PositionHolder;
import androidx.media3.extractor.SeekMap;
import androidx.media3.extractor.TrackOutput;
import androidx.media3.extractor.ts.TsExtractor;
import java.io.IOException;
import org.checkerframework.checker.nullness.qual.MonotonicNonNull;

/**
 * Extracts MPEG-2 Transport Stream (MP2T) data from RTP packets.
 *
 * <p>For MP2T over RTP (RFC2250), the RTP payload contains one or more complete TS packets (188
 * bytes each). This extractor strips the RTP header and forwards the TS payload to an internal
 * {@link TsExtractor} for demultiplexing into audio/video elementary streams.
 */
/* package */ final class Mp2tRtpExtractor implements Extractor {

  private static final String TAG = "Mp2tRtpExtractor";

  private final TsExtractor tsExtractor;
  private final ParsableByteArray rtpPacketScratchBuffer;
  private final RtpPacketReorderingQueue reorderingQueue;
  private final byte[] tsDataBuffer;
  private final TsBufferExtractorInput tsExtractorInput;

  private @MonotonicNonNull ExtractorOutput output;
  private boolean firstPacketRead;
  private volatile long firstTimestamp;
  private volatile int firstSequenceNumber;

  @GuardedBy("lock")
  private boolean isSeekPending;

  @GuardedBy("lock")
  private long nextRtpTimestamp;

  @GuardedBy("lock")
  private long playbackStartTimeUs;

  private final Object lock = new Object();

  public Mp2tRtpExtractor(int trackId) {
    this.tsExtractor = new TsExtractor();
    rtpPacketScratchBuffer = new ParsableByteArray(RtpPacket.MAX_SIZE);
    reorderingQueue = new RtpPacketReorderingQueue();
    tsDataBuffer = new byte[RtpPacket.MAX_SIZE];
    tsExtractorInput = new TsBufferExtractorInput();
    firstTimestamp = C.TIME_UNSET;
    firstSequenceNumber = C.INDEX_UNSET;
    nextRtpTimestamp = C.TIME_UNSET;
    playbackStartTimeUs = C.TIME_UNSET;
  }

  /** Sets the timestamp of the first RTP packet to arrive. */
  public void setFirstTimestamp(long firstTimestamp) {
    this.firstTimestamp = firstTimestamp;
  }

  /** Sets the sequence number of the first RTP packet to arrive. */
  public void setFirstSequenceNumber(int firstSequenceNumber) {
    this.firstSequenceNumber = firstSequenceNumber;
  }

  /** Returns whether the first RTP packet is processed. */
  public boolean hasReadFirstRtpPacket() {
    return firstPacketRead;
  }

  /**
   * Signals when performing an RTSP seek that involves RTSP message exchange.
   *
   * <p>{@link #seek} must be called after a successful RTSP seek.
   */
  public void preSeek() {
    synchronized (lock) {
      isSeekPending = true;
    }
  }

  @Override
  public boolean sniff(ExtractorInput input) {
    throw new UnsupportedOperationException(
        "RTP packets are transmitted in a packet stream do not support sniffing.");
  }

  @Override
  public void init(ExtractorOutput output) {
    // Defer TsExtractor.init() to first read() so that all dynamic tracks are captured.
    this.output = output;
  }

  @Override
  public int read(ExtractorInput input, PositionHolder seekPosition) throws IOException {
    checkNotNull(output); // Asserts init is called.

    // Reads one RTP packet at a time.
    RtspMessageLogger.d(TAG, "read() entered, calling input.read()...");
    int bytesRead = input.read(rtpPacketScratchBuffer.getData(), 0, RtpPacket.MAX_SIZE);
    RtspMessageLogger.d(TAG, "input.read() returned: " + bytesRead);
    if (bytesRead == C.RESULT_END_OF_INPUT) {
      RtspMessageLogger.d(TAG, "input.read() returned END_OF_INPUT");
      return Extractor.RESULT_END_OF_INPUT;
    } else if (bytesRead == 0) {
      RtspMessageLogger.d(TAG, "input.read() returned 0 (no data)");
      return Extractor.RESULT_CONTINUE;
    }

    rtpPacketScratchBuffer.setPosition(0);
    rtpPacketScratchBuffer.setLimit(bytesRead);
    @Nullable RtpPacket packet = RtpPacket.parse(rtpPacketScratchBuffer);
    if (packet == null) {
      return RESULT_CONTINUE;
    }

    long packetArrivalTimeMs = SystemClock.elapsedRealtime();
    long packetCutoffTimeMs = getCutoffTimeMs(packetArrivalTimeMs);
    reorderingQueue.offer(packet, packetArrivalTimeMs);
    @Nullable RtpPacket dequeuedPacket = reorderingQueue.poll(packetCutoffTimeMs);
    if (dequeuedPacket == null) {
      return RESULT_CONTINUE;
    }
    packet = dequeuedPacket;

    if (!firstPacketRead) {
      if (firstTimestamp == C.TIME_UNSET) {
        firstTimestamp = packet.timestamp;
      }
      if (firstSequenceNumber == C.INDEX_UNSET) {
        firstSequenceNumber = packet.sequenceNumber;
      }

      // Initialize TsExtractor on first packet, so its dynamic track creation goes through
      // our output wrapper.
      RtspMessageLogger.d(TAG, "First RTP packet received, initializing TsExtractor. timestamp="
          + packet.timestamp + " seq=" + packet.sequenceNumber
          + " payloadLen=" + packet.payloadData.length);
      tsExtractor.init(new TsExtractorOutputWrapper(output));
      firstPacketRead = true;
    }

    synchronized (lock) {
      if (isSeekPending) {
        if (nextRtpTimestamp != C.TIME_UNSET && playbackStartTimeUs != C.TIME_UNSET) {
          RtspMessageLogger.d(TAG, "Performing seek to ts=" + nextRtpTimestamp + " posUs=" + playbackStartTimeUs);
          reorderingQueue.reset();
          tsExtractor.seek(/* position= */ 0, /* timeUs= */ playbackStartTimeUs);
          isSeekPending = false;
          nextRtpTimestamp = C.TIME_UNSET;
          playbackStartTimeUs = C.TIME_UNSET;
        }
      } else {
        do {
          // Feed the TS payload (RTP payload contains complete TS packets) to TsExtractor.
          byte[] payload = packet.payloadData;
          if (payload.length > 0) {
            tsExtractorInput.reset(payload, payload.length);
            int tsReadResult;
            do {
              tsReadResult =
                  tsExtractor.read(tsExtractorInput, /* seekPosition= */ new PositionHolder());
            } while (tsReadResult == RESULT_CONTINUE);
          } else {
            RtspMessageLogger.w(TAG, "Received RTP packet with empty payload, seq=" + packet.sequenceNumber);
          }
          packet = reorderingQueue.poll(packetCutoffTimeMs);
        } while (packet != null);
      }
    }
    return RESULT_CONTINUE;
  }

  @Override
  public void seek(long nextRtpTimestamp, long playbackStartTimeUs) {
    synchronized (lock) {
      if (!isSeekPending) {
        isSeekPending = true;
      }
      this.nextRtpTimestamp = nextRtpTimestamp;
      this.playbackStartTimeUs = playbackStartTimeUs;
    }
  }

  @Override
  public void release() {
    tsExtractor.release();
  }

  private static long getCutoffTimeMs(long packetArrivalTimeMs) {
    return packetArrivalTimeMs - 30;
  }

  /**
   * Wraps an {@link ExtractorOutput} to delegate all calls. The TsExtractor's tracks are routed
   * through this wrapper to the external output.
   */
  private static final class TsExtractorOutputWrapper implements ExtractorOutput {

    private final ExtractorOutput delegate;

    public TsExtractorOutputWrapper(ExtractorOutput delegate) {
      this.delegate = delegate;
    }

    @Override
    public TrackOutput track(int id, int type) {
      return delegate.track(id, type);
    }

    @Override
    public void endTracks() {
      delegate.endTracks();
    }

    @Override
    public void seekMap(SeekMap seekMap) {
      delegate.seekMap(seekMap);
    }
  }

  /**
   * A byte-array-backed {@link ExtractorInput} used to feed TS data to the internal {@link
   * TsExtractor}.
   */
  private static final class TsBufferExtractorInput implements ExtractorInput {

    private byte[] data;
    private int position;
    private int length;

    public void reset(byte[] data, int length) {
      this.data = data;
      this.position = 0;
      this.length = length;
    }

    @Override
    public int read(byte[] buffer, int offset, int readLength) {
      int bytesAvailable = length - position;
      if (bytesAvailable == 0) {
        return C.RESULT_END_OF_INPUT;
      }
      int bytesToRead = Math.min(readLength, bytesAvailable);
      System.arraycopy(data, position, buffer, offset, bytesToRead);
      position += bytesToRead;
      return bytesToRead;
    }

    @Override
    public void readFully(byte[] buffer, int offset, int readLength) throws IOException {
      int bytesAvailable = length - position;
      if (bytesAvailable < readLength) {
        throw new IOException("End of input");
      }
      System.arraycopy(data, position, buffer, offset, readLength);
      position += readLength;
    }

    @Override
    public boolean readFully(byte[] target, int offset, int length, boolean allowEndOfInput) {
      int bytesAvailable = this.length - position;
      if (bytesAvailable < length) {
        if (allowEndOfInput) {
          return false;
        }
        return false;
      }
      System.arraycopy(data, position, target, offset, length);
      position += length;
      return true;
    }

    @Override
    public int peek(byte[] target, int offset, int length) {
      int bytesAvailable = this.length - position;
      if (bytesAvailable == 0) {
        return C.RESULT_END_OF_INPUT;
      }
      int bytesToRead = Math.min(length, bytesAvailable);
      System.arraycopy(data, position, target, offset, bytesToRead);
      return bytesToRead;
    }

    @Override
    public int skip(int skipLength) {
      int bytesAvailable = length - position;
      int bytesToSkip = Math.min(skipLength, bytesAvailable);
      position += bytesToSkip;
      return bytesToSkip;
    }

    @Override
    public boolean skipFully(int skipLength, boolean allowEndOfInput) {
      int bytesAvailable = length - position;
      if (bytesAvailable < skipLength) {
        if (allowEndOfInput) {
          return false;
        }
        return false;
      }
      position += skipLength;
      return true;
    }

    @Override
    public void skipFully(int skipLength) throws IOException {
      int bytesAvailable = length - position;
      if (bytesAvailable < skipLength) {
        throw new IOException("End of input");
      }
      position += skipLength;
    }

    @Override
    public boolean peekFully(byte[] buffer, int offset, int readLength, boolean allowEndOfInput) {
      int bytesAvailable = length - position;
      if (bytesAvailable < readLength) {
        if (allowEndOfInput) {
          return false;
        }
        return false;
      }
      System.arraycopy(data, position, buffer, offset, readLength);
      return true;
    }

    @Override
    public void peekFully(byte[] buffer, int offset, int readLength) throws IOException {
      if (!peekFully(buffer, offset, readLength, /* allowEndOfInput= */ false)) {
        throw new IOException("End of input");
      }
    }

    @Override
    public boolean advancePeekPosition(int peekLength, boolean allowEndOfInput) {
      return true;
    }

    @Override
    public void advancePeekPosition(int peekLength) {
      // Peek not supported; no-op.
    }

    @Override
    public void resetPeekPosition() {
      // Peek not supported; no-op.
    }

    @Override
    public long getPeekPosition() {
      return position;
    }

    @Override
    public long getPosition() {
      return position;
    }

    @Override
    public long getLength() {
      return length;
    }

    @Override
    public <E extends Throwable> void setRetryPosition(long position, E e) throws E {
      // No retry logic needed.
    }
  }
}