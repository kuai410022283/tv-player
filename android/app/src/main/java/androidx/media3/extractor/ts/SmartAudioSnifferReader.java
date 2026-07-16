package androidx.media3.extractor.ts;

import androidx.annotation.Nullable;
import androidx.media3.common.C;
import androidx.media3.common.ParserException;
import androidx.media3.common.util.ParsableByteArray;
import androidx.media3.extractor.ExtractorOutput;
import androidx.media3.extractor.ts.TsPayloadReader.TrackIdGenerator;

/**
 * A smart sniffing ElementaryStreamReader that identifies the true format of the audio stream
 * (e.g. AC-3, DTS, AV3A) by inspecting the first few bytes. Once identified, it dynamically
 * instantiates the correct reader and delegates all future calls to it, ensuring zero overhead.
 */
public final class SmartAudioSnifferReader implements ElementaryStreamReader {

    /** Callback interface to report sniff results for caching. */
    public interface SniffResultListener {
        /** Called when sniffing determines the format. {@code isAv3a} is true for AV3A, false for others. */
        void onSniffResult(boolean isAv3a);
    }

    private final String language;
    private final int roleFlags;
    @Nullable private final SniffResultListener sniffResultListener;

    private ElementaryStreamReader delegate;
    private ExtractorOutput extractorOutput;
    
    private int trackId;
    private String formatId;
    private int idIncrement = 1;

    // Buffer for sniffing. 1024 bytes is enough to find standard sync words.
    private final ParsableByteArray buffer = new ParsableByteArray(new byte[1024], 1024);
    private int bufferLength = 0;
    
    private boolean isSniffing = true;
    private long pesTimeUs = C.TIME_UNSET;
    private @TsPayloadReader.Flags int pesFlags;

    public SmartAudioSnifferReader(@Nullable String language, @C.RoleFlags int roleFlags) {
        this(language, roleFlags, null);
    }

    public SmartAudioSnifferReader(@Nullable String language, @C.RoleFlags int roleFlags,
                                    @Nullable SniffResultListener sniffResultListener) {
        this.language = language;
        this.roleFlags = roleFlags;
        this.sniffResultListener = sniffResultListener;
    }

    @Override
    public void seek() {
        if (delegate != null) {
            delegate.seek();
        } else {
            isSniffing = true;
            bufferLength = 0;
            pesTimeUs = C.TIME_UNSET;
        }
    }

    @Override
    public void createTracks(ExtractorOutput extractorOutput, TrackIdGenerator idGenerator) {
        this.extractorOutput = extractorOutput;
        // Generate our ID so it's reserved for the underlying reader later.
        idGenerator.generateNewId();
        this.trackId = idGenerator.getTrackId();
        this.formatId = idGenerator.getFormatId();
    }

    @Override
    public void packetStarted(long pesTimeUs, @TsPayloadReader.Flags int flags) {
        if (delegate != null) {
            delegate.packetStarted(pesTimeUs, flags);
        } else {
            if (this.pesTimeUs == C.TIME_UNSET) {
                this.pesTimeUs = pesTimeUs;
                this.pesFlags = flags;
            }
        }
    }

    @Override
    public void consume(ParsableByteArray data) throws ParserException {
        if (delegate != null) {
            delegate.consume(data);
            return;
        }

        int bytesAvailable = data.bytesLeft();
        int spaceLeft = buffer.capacity() - bufferLength;
        int copyLength = Math.min(bytesAvailable, spaceLeft);
        
        System.arraycopy(data.getData(), data.getPosition(), buffer.getData(), bufferLength, copyLength);
        bufferLength += copyLength;
        data.setPosition(data.getPosition() + copyLength);

        // Try sniffing if we have enough bytes
        if (bufferLength >= 4) {
            sniffAndDelegate();
        }
        
        // If still sniffing and buffer is full, fallback to default AC-3 to prevent stalling
        if (delegate == null && bufferLength >= buffer.capacity()) {
            if (sniffResultListener != null) sniffResultListener.onSniffResult(false);
            initDelegate(new Ac3Reader(language));
        }

        if (delegate != null) {
            // Replay the buffered data
            buffer.setPosition(0);
            buffer.setLimit(bufferLength);
            delegate.consume(buffer);
            bufferLength = 0; // Clear buffer to release logical state

            // Consume whatever is left in the original data payload
            if (data.bytesLeft() > 0) {
                delegate.consume(data);
            }
        }
    }

    @Override
    public void packetFinished(boolean isEndOfInput) {
        if (delegate != null) {
            delegate.packetFinished(isEndOfInput);
        }
    }

    private void sniffAndDelegate() {
        byte[] data = buffer.getData();
        // Scan for common audio sync words at every position.
        // AC-3 sync word (0x0B77) is very specific and won't false-positive.
        for (int i = 0; i < bufferLength - 1; i++) {
            if (data[i] == 0x0B && data[i + 1] == 0x77) {
                if (sniffResultListener != null) sniffResultListener.onSniffResult(false);
                initDelegate(new Ac3Reader(language));
                return;
            }
        }
        // AV3A sync word (0xFF Fx) is extremely loose — 0xFF is very common in binary data.
        // Only check at position 0 (the start of the PES payload), because an AV3A frame
        // MUST start with 0xFF 0xF0. Checking at arbitrary positions causes frequent
        // false positives on non-AV3A TS streams, which triggers the Av3aLibrary to load
        // ~12MB of native libraries (~3s delay on ARM devices).
        if (bufferLength >= 2 && data[0] == (byte) 0xFF && (data[1] & 0xF0) == 0xF0) {
            if (sniffResultListener != null) sniffResultListener.onSniffResult(true);
            initDelegate(new Av3aReader(language, roleFlags));
        }
    }

    private void initDelegate(ElementaryStreamReader reader) {
        delegate = reader;
        
        // Create a new TrackIdGenerator to pass our reserved trackId down
        // When delegate calls fakeGenerator.generateNewId(), it will assign exactly 'trackId'
        TrackIdGenerator fakeGenerator = new TrackIdGenerator(trackId, 1);
        
        delegate.createTracks(extractorOutput, fakeGenerator);
        
        if (pesTimeUs != C.TIME_UNSET) {
            delegate.packetStarted(pesTimeUs, pesFlags);
        }
    }
}
