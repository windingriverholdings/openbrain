"""OpenBrain configuration — loaded from environment variables or .env file."""

from pydantic_settings import BaseSettings, SettingsConfigDict
from pydantic import Field


class Config(BaseSettings):
    model_config = SettingsConfigDict(
        env_prefix="OPENBRAIN_",
        env_file=".env",
        env_file_encoding="utf-8",
        case_sensitive=False,
    )

    # Database
    db_host: str = Field(default="localhost")
    db_port: int = Field(default=5432)
    db_name: str = Field(default="openbrain")
    db_user: str = Field(default="openbrain")
    db_password: str = Field(default="")

    # Embedding model (fastembed)
    embedding_model: str = Field(default="BAAI/bge-small-en-v1.5")
    embedding_dim: int = Field(default=384)

    # MCP server
    mcp_server_name: str = Field(default="openbrain")
    mcp_server_version: str = Field(default="0.1.0")

    # Retrieval defaults
    search_top_k: int = Field(default=10)
    search_score_threshold: float = Field(default=0.35)

    # Telegram bot (work branch — direct, no OpenClaw)
    telegram_bot_token: str = Field(default="")
    telegram_allowed_user_id: int = Field(default=0)

    # Web UI
    web_host: str = Field(default="127.0.0.1")
    web_port: int = Field(default=10203)

    @property
    def db_url(self) -> str:
        return (
            f"postgresql://{self.db_user}:{self.db_password}"
            f"@{self.db_host}:{self.db_port}/{self.db_name}"
        )


_config: Config | None = None


def get_config() -> Config:
    global _config
    if _config is None:
        _config = Config()
    return _config
