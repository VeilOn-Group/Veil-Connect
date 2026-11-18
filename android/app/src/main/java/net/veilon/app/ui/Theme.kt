package net.veilon.app.ui

import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.darkColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.ui.graphics.Color

// veil-shared/design-tokens.json. Text on buttons uses
// primary-solid (#7C3AED) for AA contrast.
object Tokens {
    val bgBase = Color(0xFF0A0A0F)
    val bgSurface = Color(0xFF12121A)
    val bgElevated = Color(0xFF1A1A26)
    val border = Color(0xFF2A2A3A)
    val primarySolid = Color(0xFF7C3AED)
    val accent = Color(0xFFA78BFA)
    val textPrimary = Color(0xFFF4F4F8)
    val textMuted = Color(0xFF9A9AB0)
    val success = Color(0xFF22C55E)
    val warning = Color(0xFFF59E0B)
    val danger = Color(0xFFEF4444)
}

@Composable
fun VeilOnTheme(content: @Composable () -> Unit) {
    MaterialTheme(
        colorScheme = darkColorScheme(
            primary = Tokens.primarySolid, onPrimary = Tokens.textPrimary,
            secondary = Tokens.accent, background = Tokens.bgBase, onBackground = Tokens.textPrimary,
            surface = Tokens.bgSurface, onSurface = Tokens.textPrimary, surfaceVariant = Tokens.bgElevated,
            onSurfaceVariant = Tokens.textMuted, outline = Tokens.border, error = Tokens.danger,
        ),
        content = content,
    )
}
