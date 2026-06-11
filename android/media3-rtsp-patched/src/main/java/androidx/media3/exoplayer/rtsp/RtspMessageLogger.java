package androidx.media3.exoplayer.rtsp;

import android.util.Log;

public final class RtspMessageLogger {
    public interface LoggerDelegate {
        void d(String tag, String message);
        void w(String tag, String message);
        void e(String tag, String message, Throwable t);
    }

    private static LoggerDelegate delegate;

    public static void setDelegate(LoggerDelegate d) {
        delegate = d;
    }

    public static void d(String tag, String message) {
        if (delegate != null) {
            delegate.d(tag, message);
        } else {
            Log.d(tag, message);
        }
    }

    public static void w(String tag, String message) {
        if (delegate != null) {
            delegate.w(tag, message);
        } else {
            Log.w(tag, message);
        }
    }

    public static void w(String tag, String message, Throwable t) {
        if (delegate != null) {
            delegate.e(tag, message, t);
        } else {
            Log.w(tag, message, t);
        }
    }

    public static void e(String tag, String message) {
        if (delegate != null) {
            delegate.e(tag, message, null);
        } else {
            Log.e(tag, message);
        }
    }

    public static void e(String tag, String message, Throwable t) {
        if (delegate != null) {
            delegate.e(tag, message, t);
        } else {
            Log.e(tag, message, t);
        }
    }
}
