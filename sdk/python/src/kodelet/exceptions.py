"""Custom exceptions for the Kodelet SDK."""


class KodeletError(Exception):
    """Base exception for all Kodelet SDK errors."""


class BinaryNotFoundError(KodeletError):
    """Raised when the kodelet binary cannot be found."""


class ConfigurationError(KodeletError):
    """Raised when there is a configuration error."""


class ConversationNotFoundError(KodeletError):
    """Raised when a conversation cannot be found."""


class QueryError(KodeletError):
    """Raised when a query fails."""
