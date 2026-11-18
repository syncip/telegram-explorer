package com.wagnermartin.telex.ui

import androidx.activity.compose.BackHandler
import androidx.annotation.OptIn
import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.foundation.background
import androidx.compose.foundation.gestures.detectTapGestures
import androidx.compose.foundation.gestures.detectTransformGestures
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.pager.HorizontalPager
import androidx.compose.foundation.pager.rememberPagerState
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.automirrored.filled.OpenInNew
import androidx.compose.material.icons.automirrored.filled.Send
import androidx.compose.material.icons.filled.Download
import androidx.compose.material.icons.filled.Info
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableFloatStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.media3.common.MediaItem
import androidx.media3.common.util.UnstableApi
import androidx.media3.datasource.okhttp.OkHttpDataSource
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.exoplayer.source.DefaultMediaSourceFactory
import androidx.media3.ui.PlayerView
import coil3.compose.SubcomposeAsyncImage
import coil3.compose.SubcomposeAsyncImageContent
import coil3.request.ImageRequest
import coil3.request.crossfade
import coil3.compose.AsyncImagePainter
import com.wagnermartin.telex.Files
import com.wagnermartin.telex.TelexApp
import com.wagnermartin.telex.data.Node
import kotlinx.coroutines.launch
import java.io.File

/** Whether the app shows a file itself instead of handing it to another app. */
fun viewable(n: Node) = n.isImage && !n.mime.contains("svg") || n.isVideo || n.isAudio

/**
 * Photos, videos and music of a folder, swiped through full screen. Photos zoom
 * with two fingers or a double tap.
 */
@Composable
fun Viewer(
    nodes: List<Node>,
    start: Node,
    fetch: suspend (Node) -> File?,
    onInfo: (Node) -> Unit,
    onClose: () -> Unit,
) {
    val context = LocalContext.current
    val app = context.applicationContext as TelexApp
    val api = app.api ?: return
    val scope = rememberCoroutineScope()
    val pager = rememberPagerState(nodes.indexOfFirst { it.id == start.id }.coerceAtLeast(0)) { nodes.size }
    var bars by remember { mutableStateOf(true) }
    var zoomed by remember { mutableStateOf(false) }

    BackHandler { onClose() }

    Box(Modifier.fillMaxSize().background(Color.Black)) {
        HorizontalPager(pager, Modifier.fillMaxSize(), userScrollEnabled = !zoomed, key = { nodes[it].id }) { page ->
            val n = nodes[page]
            when {
                n.isImage -> ZoomImage(api.contentUrl(n.id, inline = true), api.thumbUrl(n).takeIf { n.thumb }, { bars = !bars }) { zoomed = it }
                else -> MediaPage(n, api.contentUrl(n.id, inline = true), active = pager.currentPage == page, app = app)
            }
        }
        val current = nodes.getOrNull(pager.currentPage) ?: start
        AnimatedVisibility(bars, enter = fadeIn(), exit = fadeOut()) {
            Row(
                Modifier.fillMaxWidth().background(Color.Black.copy(alpha = 0.5f)).statusBarsPadding().padding(4.dp),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                IconButton(onClose) { Icon(Icons.AutoMirrored.Filled.ArrowBack, "Schließen", tint = Color.White) }
                Column(Modifier.weight(1f)) {
                    Text(current.name, color = Color.White, maxLines = 1, overflow = TextOverflow.Ellipsis)
                    Text("${pager.currentPage + 1} von ${nodes.size} · ${formatSize(current.size)}", color = Color.White.copy(alpha = 0.7f))
                }
                IconButton({ scope.launch { fetch(current)?.let { Files.send(context, listOf(it to current)) } } }) {
                    Icon(Icons.AutoMirrored.Filled.Send, "Senden an", tint = Color.White)
                }
                IconButton({ scope.launch { fetch(current)?.let { Files.open(context, it, current) } } }) {
                    Icon(Icons.AutoMirrored.Filled.OpenInNew, "Öffnen mit", tint = Color.White)
                }
                IconButton({ Files.download(context, listOf(current)) }) { Icon(Icons.Default.Download, "Herunterladen", tint = Color.White) }
                IconButton({ onInfo(current) }) { Icon(Icons.Default.Info, "Eigenschaften", tint = Color.White) }
            }
        }
    }
}

@Composable
private fun ZoomImage(url: String, preview: String?, onTap: () -> Unit, onZoom: (Boolean) -> Unit) {
    val context = LocalContext.current
    var scale by remember { mutableFloatStateOf(1f) }
    var offset by remember { mutableStateOf(Offset.Zero) }
    LaunchedEffect(scale > 1.01f) { onZoom(scale > 1.01f) }
    DisposableEffect(Unit) { onDispose { onZoom(false) } }

    SubcomposeAsyncImage(
        model = ImageRequest.Builder(context).data(url).crossfade(true).build(),
        contentDescription = null,
        contentScale = ContentScale.Fit,
        modifier = Modifier.fillMaxSize()
            .pointerInput(Unit) {
                detectTapGestures(
                    onTap = { onTap() },
                    onDoubleTap = { p ->
                        if (scale > 1.01f) {
                            scale = 1f
                            offset = Offset.Zero
                        } else {
                            scale = 2.5f
                            offset = (Offset(size.width / 2f, size.height / 2f) - p) * 1.5f
                        }
                    },
                )
            }
            .pointerInput(Unit) {
                detectTransformGestures { _, pan, zoom, _ ->
                    val next = (scale * zoom).coerceIn(1f, 6f)
                    scale = next
                    val maxX = size.width * (next - 1) / 2
                    val maxY = size.height * (next - 1) / 2
                    offset = if (next <= 1.01f) Offset.Zero else Offset(
                        (offset.x + pan.x).coerceIn(-maxX, maxX),
                        (offset.y + pan.y).coerceIn(-maxY, maxY),
                    )
                }
            }
            .graphicsLayer {
                scaleX = scale
                scaleY = scale
                translationX = offset.x
                translationY = offset.y
            },
    ) {
        val state by painter.state.collectAsState()
        if (state is AsyncImagePainter.State.Loading || state is AsyncImagePainter.State.Empty) {
            Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
                if (preview != null) {
                    coil3.compose.AsyncImage(preview, null, Modifier.fillMaxSize(), contentScale = ContentScale.Fit)
                }
                CircularProgressIndicator(Modifier.size(40.dp), color = Color.White)
            }
        } else if (state is AsyncImagePainter.State.Error) {
            Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) { Text("Bild kann nicht angezeigt werden", color = Color.White) }
        } else {
            SubcomposeAsyncImageContent()
        }
    }
}

@OptIn(UnstableApi::class)
@Composable
private fun MediaPage(n: Node, url: String, active: Boolean, app: TelexApp) {
    val context = LocalContext.current
    val player = remember {
        ExoPlayer.Builder(context)
            .setMediaSourceFactory(DefaultMediaSourceFactory(OkHttpDataSource.Factory(app.http)))
            .build()
            .apply {
                setMediaItem(MediaItem.fromUri(url))
                prepare()
            }
    }
    LaunchedEffect(active) { player.playWhenReady = active }
    DisposableEffect(Unit) { onDispose { player.release() } }
    AndroidView(
        factory = { PlayerView(it).apply { this.player = player; setShowBuffering(PlayerView.SHOW_BUFFERING_ALWAYS) } },
        modifier = Modifier.fillMaxSize(),
    )
    if (n.isAudio) {
        Box(Modifier.fillMaxSize().padding(bottom = 160.dp), contentAlignment = Alignment.Center) {
            Text(n.name, color = Color.White)
        }
    }
}
