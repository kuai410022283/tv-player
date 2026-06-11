/*
 * Copyright 2021 The Android Open Source Project
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

import static androidx.media3.common.util.Util.castNonNull;
import static androidx.media3.exoplayer.rtsp.RtspMessageUtil.checkManifestExpression;

import androidx.annotation.Nullable;
import androidx.media3.common.C;
import androidx.media3.common.ParserException;
import androidx.media3.common.util.Util;
import java.util.Calendar;
import java.util.TimeZone;
import java.util.regex.Matcher;
import java.util.regex.Pattern;

/**
 * Represent the timing (RTSP Normal Playback Time format) of an RTSP session.
 *
 * <p>Currently only NPT is supported. See RFC2326 Section 3.6 for detail of NPT.
 */
/* package */ final class RtspSessionTiming {
  /** The default session timing starting from 0.000 and indefinite length, effectively live. */
  public static final RtspSessionTiming DEFAULT =
      new RtspSessionTiming(/* startTimeMs= */ 0, /* stopTimeMs= */ C.TIME_UNSET);

  // We only support npt=xxx-[xxx], but not npt=-xxx. See RFC2326 Section 3.6.
  // Supports both npt= and npt: identifier.
  // PATCHED: allow whitespace after "npt:" and trailing whitespace for compatibility
  // with RTSP servers that send non-standard SDP range attribute format.
  // Search for "PATCHED" to locate all local modifications.
  private static final Pattern NPT_RANGE_PATTERN =
      Pattern.compile("npt[:=]\\s*([.\\d]+|now)\\s?-\\s?([.\\d]+)?\\s*");
  private static final String START_TIMING_NTP_FORMAT = "npt=%.3f-";

  // Matches clock= format: clock=YYYYMMDDTHHMMSS.FFZ[-YYYYMMDDTHHMMSS.FFZ]
  // See RFC2326 Section 3.7 for clock time format.
  private static final Pattern CLOCK_RANGE_PATTERN =
      Pattern.compile(
          "clock=(\\d{4})(\\d{2})(\\d{2})T(\\d{2})(\\d{2})(\\d{2})(?:\\.(\\d+))?Z"
              + "-(\\d{4})(\\d{2})(\\d{2})T(\\d{2})(\\d{2})(\\d{2})(?:\\.(\\d+))?Z");

  private static final long LIVE_START_TIME = 0;

  /** Parses an SDP range attribute (RFC2326 Section 3.6). */
  public static RtspSessionTiming parseTiming(String sdpRangeAttribute) throws ParserException {
    // Try npt format first
    Matcher matcher = NPT_RANGE_PATTERN.matcher(sdpRangeAttribute);
    if (matcher.matches()) {
      return parseNptTiming(matcher, sdpRangeAttribute);
    }

    // Try clock format (RFC2326 Section 3.7)
    Matcher clockMatcher = CLOCK_RANGE_PATTERN.matcher(sdpRangeAttribute);
    if (clockMatcher.matches()) {
      return parseClockTiming(clockMatcher);
    }

    // clock= format with only start time
    if (sdpRangeAttribute.startsWith("clock=")) {
      // Unrecognized clock format, treat as live
      return new RtspSessionTiming(/* startTimeMs= */ 0, /* stopTimeMs= */ C.TIME_UNSET);
    }

    checkManifestExpression(false, /* message= */ sdpRangeAttribute);
    return DEFAULT;
  }

  private static RtspSessionTiming parseNptTiming(Matcher matcher, String sdpRangeAttribute)
      throws ParserException {
    long startTimeMs;
    long stopTimeMs;

    @Nullable String startTimeString = matcher.group(1);
    checkManifestExpression(startTimeString != null, /* message= */ sdpRangeAttribute);
    if (castNonNull(startTimeString).equals("now")) {
      startTimeMs = LIVE_START_TIME;
    } else {
      startTimeMs = (long) (Float.parseFloat(startTimeString) * C.MILLIS_PER_SECOND);
    }

    @Nullable String stopTimeString = matcher.group(2);
    if (stopTimeString != null) {
      try {
        stopTimeMs = (long) (Float.parseFloat(stopTimeString) * C.MILLIS_PER_SECOND);
      } catch (NumberFormatException e) {
        throw ParserException.createForMalformedManifest(stopTimeString, e);
      }
      checkManifestExpression(stopTimeMs >= startTimeMs, /* message= */ sdpRangeAttribute);
    } else {
      stopTimeMs = C.TIME_UNSET;
    }

    return new RtspSessionTiming(startTimeMs, stopTimeMs);
  }

  /** Parses clock=YYYYMMDDTHHMMSS.FFZ-YYYYMMDDTHHMMSS.FFZ format. */
  private static RtspSessionTiming parseClockTiming(Matcher matcher) {
    long startClockMs = parseClockTimeToEpochMs(matcher, /* startGroup= */ 1);
    long endClockMs = parseClockTimeToEpochMs(matcher, /* startGroup= */ 8);

    // Convert absolute clock times to relative NPT times.
    // If both are epoch 0, treat as live.
    if (startClockMs == 0 && endClockMs == 0) {
      return new RtspSessionTiming(/* startTimeMs= */ 0, /* stopTimeMs= */ C.TIME_UNSET);
    }

    long durationMs = endClockMs - startClockMs;
    if (durationMs < 0) {
      durationMs = C.TIME_UNSET;
    }
    return new RtspSessionTiming(/* startTimeMs= */ 0, /* stopTimeMs= */ durationMs);
  }

  /**
   * Parses a single clock time string from the matcher groups.
   *
   * <p>Groups: year(2), month(2), day(2), T, hour(2), minute(2), second(2), [.fraction], Z
   */
  private static long parseClockTimeToEpochMs(Matcher matcher, int startGroup) {
    try {
      int year = Integer.parseInt(matcher.group(startGroup));
      int month = Integer.parseInt(matcher.group(startGroup + 1)) - 1; // Calendar is 0-based
      int day = Integer.parseInt(matcher.group(startGroup + 2));
      int hour = Integer.parseInt(matcher.group(startGroup + 3));
      int minute = Integer.parseInt(matcher.group(startGroup + 4));
      int second = Integer.parseInt(matcher.group(startGroup + 5));

      Calendar calendar = Calendar.getInstance(TimeZone.getTimeZone("UTC"));
      calendar.set(year, month, day, hour, minute, second);
      calendar.set(Calendar.MILLISECOND, 0);

      long ms = calendar.getTimeInMillis();

      // Handle fractional seconds (group 7 = fraction, offset from startGroup)
      @Nullable String fraction = matcher.group(startGroup + 6);
      if (fraction != null) {
        // Pad/truncate to 3 digits for milliseconds
        while (fraction.length() < 3) {
          fraction += "0";
        }
        if (fraction.length() > 3) {
          fraction = fraction.substring(0, 3);
        }
        ms += Integer.parseInt(fraction);
      }
      return ms;
    } catch (Exception e) {
      return 0;
    }
  }

  /** Gets a Range RTSP header for an RTSP PLAY request. */
  public static String getOffsetStartTimeTiming(long offsetStartTimeMs) {
    double offsetStartTimeSec = (double) offsetStartTimeMs / C.MILLIS_PER_SECOND;
    return Util.formatInvariant(START_TIMING_NTP_FORMAT, offsetStartTimeSec);
  }

  /**
   * The start time of this session, in milliseconds. When playing a live session, the start time is
   * always zero.
   */
  public final long startTimeMs;

  /**
   * The stop time of the session, in milliseconds, or {@link C#TIME_UNSET} when the stop time is
   * not set, for example when playing a live session.
   */
  public final long stopTimeMs;

  private RtspSessionTiming(long startTimeMs, long stopTimeMs) {
    this.startTimeMs = startTimeMs;
    this.stopTimeMs = stopTimeMs;
  }

  /** Tests whether the timing is live. */
  public boolean isLive() {
    return stopTimeMs == C.TIME_UNSET;
  }

  /** Gets the session duration in milliseconds. */
  public long getDurationMs() {
    return stopTimeMs - startTimeMs;
  }
}
