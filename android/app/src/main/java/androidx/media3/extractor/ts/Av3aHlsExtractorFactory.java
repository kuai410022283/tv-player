package androidx.media3.extractor.ts;

import android.net.Uri;
import androidx.annotation.Nullable;
import androidx.media3.common.Format;
import androidx.media3.common.util.TimestampAdjuster;
import androidx.media3.exoplayer.analytics.PlayerId;
import androidx.media3.exoplayer.hls.BundledHlsMediaChunkExtractor;
import androidx.media3.exoplayer.hls.DefaultHlsExtractorFactory;
import androidx.media3.exoplayer.hls.HlsExtractorFactory;
import androidx.media3.exoplayer.hls.HlsMediaChunkExtractor;
import androidx.media3.extractor.Extractor;
import androidx.media3.extractor.ExtractorInput;

import java.io.IOException;
import java.lang.reflect.Field;
import java.util.List;
import java.util.Map;

/**
 * HLS Extractor Factory that injects {@link Av3aTsPayloadReaderFactory} into TS extractors
 * created by DefaultHlsExtractorFactory, enabling AV3A / Audio Vivid decoding for HLS (.m3u8) streams
 * without altering or re-creating chunk extractors.
 */
public final class Av3aHlsExtractorFactory implements HlsExtractorFactory {

    private final DefaultHlsExtractorFactory defaultFactory = new DefaultHlsExtractorFactory();

    @Override
    public HlsMediaChunkExtractor createExtractor(
            Uri uri,
            Format format,
            @Nullable List<Format> muxedCaptionFormats,
            TimestampAdjuster timestampAdjuster,
            Map<String, List<String>> responseHeaders,
            ExtractorInput sniffingExtractorInput,
            PlayerId playerId)
            throws IOException {

        HlsMediaChunkExtractor defaultExtractor = defaultFactory.createExtractor(
                uri, format, muxedCaptionFormats, timestampAdjuster, responseHeaders, sniffingExtractorInput, playerId);

        if (defaultExtractor instanceof BundledHlsMediaChunkExtractor) {
            try {
                Field extractorField = BundledHlsMediaChunkExtractor.class.getDeclaredField("extractor");
                extractorField.setAccessible(true);
                Extractor innerExtractor = (Extractor) extractorField.get(defaultExtractor);

                if (innerExtractor instanceof TsExtractor) {
                    Field factoryField = TsExtractor.class.getDeclaredField("payloadReaderFactory");
                    factoryField.setAccessible(true);
                    factoryField.set(innerExtractor, new Av3aTsPayloadReaderFactory());
                }
            } catch (Throwable ignored) {
                // Fallback: defaultExtractor plays cleanly as standard
            }
        }

        return defaultExtractor;
    }
}
